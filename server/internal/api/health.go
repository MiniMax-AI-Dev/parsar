// Package api hosts the always-on HTTP routes.
//
// /healthz is liveness — it must never touch a dependency, since
// liveness failures restart the pod and amplify pressure on whatever
// is already struggling. /readyz is readiness — failure drains the
// pod at the LB without restarting. /api/v1/health is a legacy alias
// behaving like /healthz; dev-server-up.sh and smoke.sh curl it.
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"time"

	"github.com/go-chi/chi/v5"
)

// Pinger is the minimum surface a dependency must expose to be checked
// by /readyz. *pgxpool.Pool satisfies it via Ping(ctx).
type Pinger interface {
	Ping(ctx context.Context) error
}

// HealthDeps bundles the dependencies the readiness probe inspects.
// Every field is nil-safe so partial wiring still produces a useful
// verdict instead of a panic.
type HealthDeps struct {
	// DB nil → /readyz reports "not_configured" and 503: a Parsar
	// process with no database cannot serve real traffic.
	DB Pinger
}

type HealthResponse struct {
	Status string `json:"status"`
	Name   string `json:"name"`
}

// CheckStatus is the per-dependency state emitted by /readyz. Small
// string set so dashboards can match on exact values.
type CheckStatus string

const (
	CheckStatusOK   CheckStatus = "ok"
	CheckStatusFail CheckStatus = "fail"
	// CheckStatusNotConfigured means the dependency is not wired
	// (e.g. DATABASE_URL unset). Overall /readyz verdict is still 503.
	CheckStatusNotConfigured CheckStatus = "not_configured"
)

type CheckResult struct {
	Name   string      `json:"name"`
	Status CheckStatus `json:"status"`
	Error  string      `json:"error,omitempty"`
}

// ReadyResponse is the /readyz JSON shape. Status is "ok" iff every
// check reports CheckStatusOK; otherwise "degraded".
type ReadyResponse struct {
	Status string        `json:"status"`
	Name   string        `json:"name"`
	Checks []CheckResult `json:"checks"`
}

// readyCheckTimeout caps how long a single readiness probe waits per
// dependency. Var so tests can shrink it to drive the deadline-exceeded
// path without sleeping for whole seconds.
var readyCheckTimeout = 2 * time.Second

// RegisterHealthRoutes mounts the three health endpoints on r.
// Safe to call before any dependency is healthy: handlers query deps
// every request, so a database that comes up late flips /readyz from
// 503 to 200 on the next poll without a process restart.
func RegisterHealthRoutes(r chi.Router, deps HealthDeps) {
	r.Get("/healthz", livenessHandler())
	r.Get("/readyz", readinessHandler(deps))
	r.Get("/api/v1/health", livenessHandler())
}

// livenessHandler answers the liveness / legacy-health probe.
// Never touches any dependency — liveness failures restart the pod, so
// amplifying pressure on a struggling dep would be self-defeating.
//
//	@Summary	Health check
//	@Description	Liveness probe. Returns 200 as long as the process can serve HTTP; does not touch any dependency.
//	@Tags		health
//	@ID			getHealth
//	@Produce	json
//	@Success	200 {object} HealthResponse "Server is healthy"
//	@Router		/api/v1/health [get]
func livenessHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, HealthResponse{Status: "ok", Name: "parsar"})
	}
}

// readinessHandler answers the readiness probe. Unlike liveness, it
// pings each configured dependency; the pod is drained (not restarted)
// when any check reports non-ok, so a late-starting database flips
// the verdict back to 200 on the next poll without a restart.
//
//	@Summary		Readiness probe
//	@Description	Readiness probe. Pings each wired dependency (currently the database). Returns 200 with status "ok" iff every check reports ok; otherwise 503 with status "degraded".
//	@Tags			health
//	@ID				getReadyz
//	@Produce		json
//	@Success		200	{object}	ReadyResponse	"All dependencies healthy"
//	@Failure		503	{object}	ReadyResponse	"One or more dependencies unhealthy or not configured"
//	@Router			/readyz [get]
func readinessHandler(deps HealthDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Per-probe timeout (not per-check) so total latency stays
		// predictable as the dependency list grows. Wired to the
		// request context so a client disconnect cancels in-flight pings.
		ctx, cancel := context.WithTimeout(r.Context(), readyCheckTimeout)
		defer cancel()

		checks := []CheckResult{checkDatabase(ctx, deps.DB)}

		overall := "ok"
		code := http.StatusOK
		for _, c := range checks {
			if c.Status != CheckStatusOK {
				overall = "degraded"
				code = http.StatusServiceUnavailable
				break
			}
		}
		writeJSON(w, code, ReadyResponse{
			Status: overall,
			Name:   "parsar",
			Checks: checks,
		})
	}
}

// checkDatabase defends against the typed-nil interface pitfall: a
// caller passing a nil *pgxpool.Pool wrapped in HealthDeps.DB would
// satisfy `db != nil` but panic on db.Ping. isNilPinger normalises both
// shapes so /readyz reports a clean not_configured row instead.
func checkDatabase(ctx context.Context, db Pinger) CheckResult {
	const name = "database"
	if isNilPinger(db) {
		return CheckResult{
			Name:   name,
			Status: CheckStatusNotConfigured,
			Error:  "database pool is not initialised; check DATABASE_URL and that the database accepts connections at startup",
		}
	}
	if err := db.Ping(ctx); err != nil {
		return CheckResult{
			Name:   name,
			Status: CheckStatusFail,
			Error:  err.Error(),
		}
	}
	return CheckResult{Name: name, Status: CheckStatusOK}
}

// isNilPinger returns true for both the nil interface and an interface
// wrapping a nil pointer/map/chan/slice/func — see checkDatabase.
func isNilPinger(db Pinger) bool {
	if db == nil {
		return true
	}
	v := reflect.ValueOf(db)
	switch v.Kind() {
	case reflect.Ptr, reflect.Interface, reflect.Chan, reflect.Map, reflect.Slice, reflect.Func:
		return v.IsNil()
	}
	return false
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
