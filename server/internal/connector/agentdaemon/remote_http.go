package agentdaemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"

	"github.com/go-chi/chi/v5"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

const (
	internalStreamPromptPath              = "/internal/agent-daemon/stream-prompt"
	internalSubmitPermissionPath          = "/internal/agent-daemon/submit-permission"
	internalSubmitPromptForUserChoicePath = "/internal/agent-daemon/submit-prompt-for-user-choice"
)

type remoteStreamPromptRequest struct {
	Generation int64                 `json:"generation"`
	Input      connector.PromptInput `json:"input"`
}

type remoteSubmitPermissionRequest struct {
	Generation int64                         `json:"generation"`
	Decision   connector.PermissionDecision  `json:"decision"`
}

type remoteSubmitPromptForUserChoiceRequest struct {
	Generation int64                                  `json:"generation"`
	Decision   connector.PromptForUserChoiceDecision  `json:"decision"`
}

// HTTPRemoteStreamer forwards StreamPrompt to the pod recorded in the
// owner table. The response is newline-delimited JSON PromptEvents so
// the caller can keep the same event consumption path as a local stream.
//
// The same struct doubles as HTTPRemoteSubmitter (it implements
// SubmitPermissionRemote / SubmitPromptForUserChoiceRemote below) so
// main.go can pass one instance for both Remote and RemoteSubmit
// dependencies — Client + Token are identical.
type HTTPRemoteStreamer struct {
	Client *http.Client
	Token  string
}

func (s HTTPRemoteStreamer) StreamPromptRemote(ctx context.Context, owner store.AgentDaemonDeviceOwnerRead, in connector.PromptInput) (<-chan connector.PromptEvent, error) {
	ownerURL := strings.TrimRight(strings.TrimSpace(owner.OwnerURL), "/")
	if ownerURL == "" {
		return nil, fmt.Errorf("agent_daemon: remote owner %s has empty owner_url", owner.OwnerPodID)
	}
	body, err := json.Marshal(remoteStreamPromptRequest{Generation: owner.Generation, Input: in})
	if err != nil {
		return nil, fmt.Errorf("agent_daemon: marshal remote prompt: %w", err)
	}
	target := ownerURL + internalStreamPromptPath
	log.Bg().Info("agent_daemon: HTTP remote stream-prompt POST issuing",
		"run_id", in.RunID, "owner_pod_id", owner.OwnerPodID, "target", target, "body_bytes", len(body))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("agent_daemon: build remote prompt request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(s.Token) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(s.Token))
	}
	client := s.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		log.Bg().Error("agent_daemon: HTTP remote stream-prompt POST failed",
			"run_id", in.RunID, "owner_pod_id", owner.OwnerPodID, "target", target, "err", err.Error())
		return nil, fmt.Errorf("agent_daemon: remote prompt %s: %w", owner.OwnerPodID, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		var errBody struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		if errBody.Error == "" {
			errBody.Error = resp.Status
		}
		log.Bg().Error("agent_daemon: HTTP remote stream-prompt non-2xx",
			"run_id", in.RunID, "owner_pod_id", owner.OwnerPodID, "status", resp.StatusCode, "err", errBody.Error)
		return nil, fmt.Errorf("agent_daemon: remote prompt %s returned %s", owner.OwnerPodID, errBody.Error)
	}
	log.Bg().Info("agent_daemon: HTTP remote stream-prompt POST ok, streaming",
		"run_id", in.RunID, "owner_pod_id", owner.OwnerPodID, "status", resp.StatusCode)

	out := make(chan connector.PromptEvent, 32)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		dec := json.NewDecoder(bufio.NewReader(resp.Body))
		for {
			var ev connector.PromptEvent
			if err := dec.Decode(&ev); err != nil {
				if err == io.EOF || ctx.Err() != nil {
					return
				}
				select {
				case out <- connector.PromptEvent{Type: connector.EventError, Error: "agent_daemon: remote stream decode: " + err.Error()}:
				case <-ctx.Done():
				}
				return
			}
			select {
			case out <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

// SubmitPermissionRemote POSTs the verdict to the owner pod's internal
// submit-permission endpoint. Errors surface to the feishu webhook
// handler as "update failed" toasts so the user can retry.
func (s HTTPRemoteStreamer) SubmitPermissionRemote(ctx context.Context, owner store.AgentDaemonDeviceOwnerRead, decision connector.PermissionDecision) error {
	body, err := json.Marshal(remoteSubmitPermissionRequest{Generation: owner.Generation, Decision: decision})
	if err != nil {
		return fmt.Errorf("agent_daemon: marshal remote submit-permission: %w", err)
	}
	return s.postSubmit(ctx, owner, internalSubmitPermissionPath, "submit-permission", decision.RequestID, body)
}

// SubmitPromptForUserChoiceRemote POSTs the choice to the owner pod's
// internal submit-prompt-for-user-choice endpoint.
func (s HTTPRemoteStreamer) SubmitPromptForUserChoiceRemote(ctx context.Context, owner store.AgentDaemonDeviceOwnerRead, decision connector.PromptForUserChoiceDecision) error {
	body, err := json.Marshal(remoteSubmitPromptForUserChoiceRequest{Generation: owner.Generation, Decision: decision})
	if err != nil {
		return fmt.Errorf("agent_daemon: marshal remote submit-prompt-for-user-choice: %w", err)
	}
	return s.postSubmit(ctx, owner, internalSubmitPromptForUserChoicePath, "submit-prompt-for-user-choice", decision.RequestID, body)
}

// postSubmit is the shared POST + 2xx-or-error path for the Submit-side
// internal endpoints. Unlike StreamPromptRemote, no body parsing is
// needed on success — a 200 means the owner pod ran the registry
// lookup + send. Errors are bubbled up with the request id stamped on
// for callsite logs.
func (s HTTPRemoteStreamer) postSubmit(ctx context.Context, owner store.AgentDaemonDeviceOwnerRead, path, label, requestID string, body []byte) error {
	ownerURL := strings.TrimRight(strings.TrimSpace(owner.OwnerURL), "/")
	if ownerURL == "" {
		return fmt.Errorf("agent_daemon: remote owner %s has empty owner_url", owner.OwnerPodID)
	}
	target := ownerURL + path
	log.Bg().Info("agent_daemon: HTTP remote "+label+" POST issuing",
		"request_id", requestID, "owner_pod_id", owner.OwnerPodID, "target", target, "body_bytes", len(body))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("agent_daemon: build remote %s request: %w", label, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(s.Token) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(s.Token))
	}
	client := s.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		log.Bg().Error("agent_daemon: HTTP remote "+label+" POST failed",
			"request_id", requestID, "owner_pod_id", owner.OwnerPodID, "target", target, "err", err.Error())
		return fmt.Errorf("agent_daemon: remote %s %s: %w", label, owner.OwnerPodID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errBody struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		if errBody.Error == "" {
			errBody.Error = resp.Status
		}
		log.Bg().Error("agent_daemon: HTTP remote "+label+" non-2xx",
			"request_id", requestID, "owner_pod_id", owner.OwnerPodID, "status", resp.StatusCode, "err", errBody.Error)
		return fmt.Errorf("agent_daemon: remote %s %s returned %s", label, owner.OwnerPodID, errBody.Error)
	}
	log.Bg().Info("agent_daemon: HTTP remote "+label+" POST ok",
		"request_id", requestID, "owner_pod_id", owner.OwnerPodID, "status", resp.StatusCode)
	// Drain body so the connection can be reused.
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// RegisterInternalRoutes mounts the owner-pod endpoints consumed by
// HTTPRemoteStreamer / HTTPRemoteSubmitter. Token-gated because they
// are mounted on the same HTTP server as public API routes; deployments
// should pass a cluster-shared token derived from PARSAR_MASTER_KEY.
func RegisterInternalRoutes(r chi.Router, c *Connector, token string) {
	if c == nil {
		panic("agent_daemon internal routes: connector required")
	}
	trimmedToken := strings.TrimSpace(token)
	r.Post(internalStreamPromptPath, func(w http.ResponseWriter, req *http.Request) {
		if trimmedToken != "" && bearerFromAuth(req) != trimmedToken {
			writeRemoteError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		var body remoteStreamPromptRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeRemoteError(w, http.StatusBadRequest, "bad_json")
			return
		}
		if err := c.assertLocalOwner(req.Context(), body.Input, body.Generation); err != nil {
			writeRemoteError(w, http.StatusConflict, err.Error())
			return
		}
		events, err := c.StreamPromptLocal(req.Context(), body.Input)
		if err != nil {
			writeRemoteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		flush, _ := w.(http.Flusher)
		enc := json.NewEncoder(w)
		for ev := range events {
			if err := enc.Encode(ev); err != nil {
				return
			}
			if flush != nil {
				flush.Flush()
			}
		}
	})

	r.Post(internalSubmitPermissionPath, func(w http.ResponseWriter, req *http.Request) {
		if trimmedToken != "" && bearerFromAuth(req) != trimmedToken {
			writeRemoteError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		var body remoteSubmitPermissionRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeRemoteError(w, http.StatusBadRequest, "bad_json")
			return
		}
		if err := c.assertLocalOwnerForSubmit(req.Context(), body.Decision.RequestID, body.Generation, "permission"); err != nil {
			writeRemoteError(w, http.StatusConflict, err.Error())
			return
		}
		if err := c.SubmitPermissionLocal(req.Context(), body.Decision); err != nil {
			writeRemoteError(w, http.StatusBadRequest, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	r.Post(internalSubmitPromptForUserChoicePath, func(w http.ResponseWriter, req *http.Request) {
		if trimmedToken != "" && bearerFromAuth(req) != trimmedToken {
			writeRemoteError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		var body remoteSubmitPromptForUserChoiceRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeRemoteError(w, http.StatusBadRequest, "bad_json")
			return
		}
		if err := c.assertLocalOwnerForSubmit(req.Context(), body.Decision.RequestID, body.Generation, "prompt_for_user_choice"); err != nil {
			writeRemoteError(w, http.StatusConflict, err.Error())
			return
		}
		if err := c.SubmitPromptForUserChoiceLocal(req.Context(), body.Decision); err != nil {
			writeRemoteError(w, http.StatusBadRequest, err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
}

func (c *Connector) assertLocalOwner(ctx context.Context, in connector.PromptInput, generation int64) error {
	if generation <= 0 {
		return fmt.Errorf("stale_owner: missing generation")
	}
	if c.ownerResolver == nil {
		return nil
	}
	bind, err := c.binder.Resolve(ctx, in.ConversationID, in.ProjectAgentID)
	if err != nil {
		return fmt.Errorf("stale_owner: resolve binding: %w", err)
	}
	owner, ok, err := c.ownerResolver.GetAgentDaemonDeviceOwner(ctx, bind.DeviceID)
	if err != nil {
		return fmt.Errorf("stale_owner: resolve owner: %w", err)
	}
	if !ok || owner.Status != store.AgentDaemonOwnerStatusConnected || !owner.LeaseExpiresAt.After(time.Now().UTC()) {
		return fmt.Errorf("stale_owner: owner lease is not active")
	}
	if owner.OwnerPodID != c.ownerPodID || owner.Generation != generation {
		return fmt.Errorf("stale_owner: owner=%s generation=%d", owner.OwnerPodID, owner.Generation)
	}
	return nil
}

// assertLocalOwnerForSubmit mirrors assertLocalOwner but resolves the
// device id from the inflight slot — Submit requests carry only a
// request id over the wire, not the conversation/project_agent tuple.
// A nil submitSlots is treated as "single-pod / tests" and skips the
// check, matching ownerResolver semantics.
func (c *Connector) assertLocalOwnerForSubmit(ctx context.Context, requestID string, generation int64, kind string) error {
	if c.ownerResolver == nil || c.submitSlots == nil {
		return nil
	}
	if generation <= 0 {
		return fmt.Errorf("stale_owner: missing generation")
	}
	var (
		deviceID string
		err      error
	)
	switch kind {
	case "permission":
		deviceID, err = c.submitSlots.DeviceIDForPermissionRequest(ctx, requestID)
	case "prompt_for_user_choice":
		deviceID, err = c.submitSlots.DeviceIDForPromptForUserChoiceRequest(ctx, requestID)
	default:
		return fmt.Errorf("stale_owner: unknown submit kind %q", kind)
	}
	if err != nil {
		return fmt.Errorf("stale_owner: resolve device for %s: %w", kind, err)
	}
	if strings.TrimSpace(deviceID) == "" {
		// Slot has no device id (legacy row) — accept and let the local
		// registry lookup decide. The local lookup would fail with a
		// clean NotRegistered if this pod is not the owner.
		return nil
	}
	owner, ok, err := c.ownerResolver.GetAgentDaemonDeviceOwner(ctx, deviceID)
	if err != nil {
		return fmt.Errorf("stale_owner: resolve owner: %w", err)
	}
	if !ok || owner.Status != store.AgentDaemonOwnerStatusConnected || !owner.LeaseExpiresAt.After(time.Now().UTC()) {
		return fmt.Errorf("stale_owner: owner lease is not active")
	}
	if owner.OwnerPodID != c.ownerPodID || owner.Generation != generation {
		return fmt.Errorf("stale_owner: owner=%s generation=%d", owner.OwnerPodID, owner.Generation)
	}
	return nil
}

func bearerFromAuth(r *http.Request) string {
	raw := r.Header.Get("Authorization")
	if !strings.HasPrefix(raw, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(raw, "Bearer "))
}

func writeRemoteError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
