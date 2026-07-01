package dev

// Per-binding sandbox connectivity test for the "test connection" button.
//
// agent_daemon sandboxes don't expose an HTTP endpoint — the daemon dials
// back over a reverse WebSocket — so "is this sandbox reachable" reduces
// to "is the daemon's runtime row alive": liveness == online and
// last_heartbeat_at is recent.
//
// Two checks (pipeline; later checks skipped after an earlier failure):
//   daemon_paired — binding.metadata.device_id points at a runtime row.
//   daemon_online — that runtime's liveness == "online" and heartbeat
//                   is within the freshness window.

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/audit"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

const (
	sandboxConnectivityCheckPaired = "daemon_paired"
	sandboxConnectivityCheckOnline = "daemon_online"
)

// Error categories — stable wire values; never echo raw vendor errors.
// Maps to web `errorCategories` i18n block.
const (
	tryConnectionErrUnreachable = "unreachable"
	tryConnectionErrRuntimeDown = "runtimeDown"
	tryConnectionErrUnknown     = "unknown"
)

// AuditIngester is the audit dependency for dev handlers that emit audit
// events directly. Defined locally so handlers don't take a concrete
// *audit.Ingester (keeps test fakes one-liner).
type AuditIngester interface {
	Emit(ev audit.Event) error
}

// Compile-time assertion: *audit.Ingester satisfies AuditIngester.
var _ AuditIngester = (*audit.Ingester)(nil)

// sandboxConnectivityHeartbeatFreshness is the max age of last_heartbeat_at
// for daemon_online to pass. Safely larger than the daemon's heartbeat
// interval (~30s) plus one sweep cycle.
var sandboxConnectivityHeartbeatFreshness = 90 * time.Second

// sandboxConnectivityCheckError is the per-check error object sent to
// the frontend.
type sandboxConnectivityCheckError struct {
	Category string `json:"category"`
	Detail   string `json:"detail,omitempty"`
}

// sandboxConnectivityCheck is one row of the response checks array.
type sandboxConnectivityCheck struct {
	Name       string                         `json:"name"`
	Pass       bool                           `json:"pass"`
	DurationMs int64                          `json:"duration_ms"`
	Error      *sandboxConnectivityCheckError `json:"error"`
}

type sandboxConnectivityResponse struct {
	Overall    string                     `json:"overall"`
	StartedAt  string                     `json:"started_at"`
	DurationMs int64                      `json:"duration_ms"`
	SandboxID  string                     `json:"sandbox_id"`
	Checks     []sandboxConnectivityCheck `json:"checks"`
}

func sandboxConnectivityTest(deps sandboxAdminDeps, ingester AuditIngester) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if deps.store == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "sandbox lifecycle store not wired",
			})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		agentID := strings.TrimSpace(chi.URLParam(r, "agentID"))
		if !isUUID(workspaceID) || !isUUID(agentID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "workspace_id and agent_id must be UUIDs",
			})
			return
		}

		binding, found, err := deps.store.GetActiveSandboxBindingForAgent(
			r.Context(), workspaceID, agentID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": "lookup failed: " + err.Error(),
			})
			return
		}
		if !found {
			writeJSON(w, http.StatusNotFound, map[string]string{
				"error":        "no active sandbox binding",
				"workspace_id": workspaceID,
				"agent_id":     agentID,
			})
			return
		}

		startedAt := time.Now().UTC()
		checks := runSandboxConnectivityChecks(r.Context(), deps.store, binding, startedAt)
		totalDuration := time.Since(startedAt)

		overall := computeSandboxConnectivityOverall(checks)
		resp := sandboxConnectivityResponse{
			Overall:    overall,
			StartedAt:  startedAt.Format(time.RFC3339),
			DurationMs: totalDuration.Milliseconds(),
			SandboxID:  binding.SandboxID,
			Checks:     checks,
		}

		emitSandboxConnectivityAudit(ingester, r, workspaceID, agentID, startedAt, totalDuration, resp)

		writeJSON(w, http.StatusOK, resp)
	}
}

// runSandboxConnectivityChecks runs the two daemon checks in order; later
// checks are skipped after an earlier failure.
func runSandboxConnectivityChecks(
	ctx context.Context,
	bindings SandboxBindingStore,
	binding store.SandboxBindingRead,
	now time.Time,
) []sandboxConnectivityCheck {
	checks := make([]sandboxConnectivityCheck, 0, 2)

	deviceID, _ := binding.Metadata["device_id"].(string)
	pairedStart := time.Now()
	if strings.TrimSpace(deviceID) == "" {
		checks = append(checks, sandboxConnectivityCheck{
			Name:       sandboxConnectivityCheckPaired,
			Pass:       false,
			DurationMs: time.Since(pairedStart).Milliseconds(),
			Error: &sandboxConnectivityCheckError{
				Category: tryConnectionErrRuntimeDown,
				Detail:   "binding metadata has no device_id; sandbox is orphaned",
			},
		})
		return checks
	}

	runtime, ok, err := bindings.GetRuntime(ctx, deviceID)
	pairedDur := time.Since(pairedStart)
	if err != nil {
		checks = append(checks, sandboxConnectivityCheck{
			Name:       sandboxConnectivityCheckPaired,
			Pass:       false,
			DurationMs: pairedDur.Milliseconds(),
			Error: &sandboxConnectivityCheckError{
				Category: tryConnectionErrUnknown,
				Detail:   "runtime lookup failed: " + err.Error(),
			},
		})
		return checks
	}
	if !ok {
		checks = append(checks, sandboxConnectivityCheck{
			Name:       sandboxConnectivityCheckPaired,
			Pass:       false,
			DurationMs: pairedDur.Milliseconds(),
			Error: &sandboxConnectivityCheckError{
				Category: tryConnectionErrRuntimeDown,
				Detail:   "agent_daemon runtime row not found; sandbox was likely killed",
			},
		})
		return checks
	}
	checks = append(checks, sandboxConnectivityCheck{
		Name:       sandboxConnectivityCheckPaired,
		Pass:       true,
		DurationMs: pairedDur.Milliseconds(),
	})

	onlineStart := time.Now()
	if runtime.Liveness != store.RuntimeLivenessOnline {
		checks = append(checks, sandboxConnectivityCheck{
			Name:       sandboxConnectivityCheckOnline,
			Pass:       false,
			DurationMs: time.Since(onlineStart).Milliseconds(),
			Error: &sandboxConnectivityCheckError{
				Category: tryConnectionErrUnreachable,
				Detail:   "agent_daemon liveness=" + runtime.Liveness,
			},
		})
		return checks
	}
	if runtime.LastHeartbeatAt == nil ||
		now.Sub(*runtime.LastHeartbeatAt) > sandboxConnectivityHeartbeatFreshness {
		checks = append(checks, sandboxConnectivityCheck{
			Name:       sandboxConnectivityCheckOnline,
			Pass:       false,
			DurationMs: time.Since(onlineStart).Milliseconds(),
			Error: &sandboxConnectivityCheckError{
				Category: tryConnectionErrUnreachable,
				Detail:   "agent_daemon last_heartbeat_at stale",
			},
		})
		return checks
	}
	checks = append(checks, sandboxConnectivityCheck{
		Name:       sandboxConnectivityCheckOnline,
		Pass:       true,
		DurationMs: time.Since(onlineStart).Milliseconds(),
	})

	return checks
}

func computeSandboxConnectivityOverall(checks []sandboxConnectivityCheck) string {
	if len(checks) == 0 {
		return "fail"
	}
	allPass := true
	anyPass := false
	for _, c := range checks {
		if c.Pass {
			anyPass = true
		} else {
			allPass = false
		}
	}
	if allPass {
		return "pass"
	}
	if anyPass {
		return "partial"
	}
	return "fail"
}

func emitSandboxConnectivityAudit(ingester AuditIngester, r *http.Request, workspaceID, agentID string, startedAt time.Time, duration time.Duration, resp sandboxConnectivityResponse) {
	if ingester == nil {
		return
	}
	checkSummary := make([]map[string]any, 0, len(resp.Checks))
	for _, c := range resp.Checks {
		entry := map[string]any{
			"name":        c.Name,
			"pass":        c.Pass,
			"duration_ms": c.DurationMs,
		}
		if c.Error != nil {
			entry["error"] = c.Error.Category
		}
		checkSummary = append(checkSummary, entry)
	}
	_ = ingester.Emit(audit.Event{
		OccurredAt:  startedAt,
		Source:      audit.SourceAdmin,
		EventType:   "runtime.sandbox_connectivity_test",
		ActorType:   audit.ActorTypeUser,
		ActorID:     actorIDFromRequest(r),
		TargetType:  "sandbox_binding",
		TargetID:    resp.SandboxID,
		WorkspaceID: workspaceID,
		Payload: map[string]any{
			"overall":     resp.Overall,
			"sandbox_id":  resp.SandboxID,
			"duration_ms": duration.Milliseconds(),
			"agent_id":    agentID,
			"checks":      checkSummary,
		},
	})
}
