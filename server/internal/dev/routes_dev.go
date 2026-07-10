package dev

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"net/http"
	"os"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/httprunner"
	e2bsandbox "github.com/MiniMax-AI-Dev/parsar/server/internal/sandbox/e2b"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
)

func getSeed(w http.ResponseWriter, r *http.Request) {
	// SeedData is the human-readable fixture (back-compat with
	// existing dev consumers). The `db` key carries the real DB UUIDs
	// `cmd/seeddev` writes so the admin frontend can auto-bind.
	seed := DefaultSeed()
	ids := store.DefaultDevFixtureIDs()
	writeJSON(w, http.StatusOK, map[string]any{
		"workspace":     seed.Workspace,
		"users":         seed.Users,
		"agents":        seed.Agents,
		"conversations": seed.Conversations,
		// Deterministic DB UUIDs from store.DefaultDevFixtureIDs —
		// match exactly what `make seed-dev-db` inserts.
		"db": map[string]any{
			"workspace_id":    ids.WorkspaceID,
			"user_id":         ids.UserID,
			"conversation_id": ids.ConversationID,
			"agents": map[string]string{
				"product_agent_id": ids.ProductAgentID,
				"backend_agent_id": ids.BackendAgentID,
				"test_agent_id":    ids.TestAgentID,
			},
		},
	})
}

type verifyRequest struct {
	Email string `json:"email"`
	Code  string `json:"code"`
}

// verifyDevAuth is POST /dev/auth/verify. Dev-only fake login: accepts a
// {email, code} body where code must equal DevVerificationCode, then hands
// back a dev bearer token + user shape mirroring the real auth flow so
// smoke tests can bypass Feishu OIDC entirely.
//
//	@Summary		Dev-only email + code login
//	@Description	Development-only login. Verifies the fixed dev code and returns a bearer token plus the default seed workspace + user. Never enable in production.
//	@Tags			dev
//	@ID				verifyDevAuth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		map[string]string		true	"{email, code}"
//	@Success		200		{object}	map[string]interface{}	"Bearer token + user + workspace"
//	@Failure		400		{object}	map[string]string		"Invalid json"
//	@Failure		401		{object}	map[string]string		"Invalid dev credentials"
//	@Router			/dev/auth/verify [post]
func verifyDevAuth(w http.ResponseWriter, r *http.Request) {
	var req verifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	if strings.TrimSpace(req.Email) == "" || req.Code != DevVerificationCode {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid dev credentials"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":        "dev-token",
		"token_type":   "Bearer",
		"workspace_id": DefaultSeed().Workspace.ID,
		"user": map[string]string{
			"id":    "dev_admin",
			"email": req.Email,
			"name":  "Dev Admin",
		},
	})
}

type httpAgentInvokeBody struct {
	Endpoint string            `json:"endpoint"`
	Headers  map[string]string `json:"headers"`
}

// invokeHTTPAgentRun invokes a pending HTTP-agent run row.
//
//	@Summary		Invoke an HTTP agent run
//	@Description	Executes the HTTP-agent invocation associated with the given run row. Development helper.
//	@Tags			dev
//	@ID				invokeDevHTTPAgentRun
//	@Accept			json
//	@Produce		json
//	@Param			runID	path	string				true	"Run UUID"
//	@Param			body	body	httpAgentInvokeBody	true	"Invocation payload"
//	@Success		200 {object} map[string]interface{} "Run result"
//	@Failure		400 {object} map[string]string "Invalid body or UUID"
//	@Failure		404 {object} map[string]string "Run not found"
//	@Router			/dev/http-agent/runs/{runID}/invoke [post]
func invokeHTTPAgentRun(runtimeStore RuntimeStore, client *http.Client, deps *httprunner.Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed http agent connector is disabled"})
			return
		}

		runID := strings.TrimSpace(chi.URLParam(r, "runID"))
		if !isUUID(runID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "run_id must be a valid uuid"})
			return
		}

		var req httpAgentInvokeBody
		if r.Body != nil {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
				return
			}
		}

		runHTTPAgentInvocation(w, r, runtimeStore, client, runID, req, deps)
	}
}

// runHTTPAgentOnce runs an HTTP-agent workload once, synchronously.
//
//	@Summary		Run an HTTP agent once
//	@Description	Executes a single HTTP-agent invocation synchronously and returns the result. Development helper.
//	@Tags			dev
//	@ID				runDevHTTPAgentOnce
//	@Accept			json
//	@Produce		json
//	@Param			body	body	map[string]interface{}	true	"Run payload"
//	@Success		200 {object} map[string]interface{} "Run result"
//	@Failure		400 {object} map[string]string "Invalid body"
//	@Router			/dev/http-agent/runner/run-once [post]
func runHTTPAgentOnce(runtimeStore RuntimeStore, client *http.Client, deps *httprunner.Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed http runner is disabled"})
			return
		}

		result, err := httprunner.RunOnce(r.Context(), runtimeStore, client, deps)
		if err != nil {
			switch {
			case errors.Is(err, store.ErrUnknownAgentRun):
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			case errors.Is(err, store.ErrInvalidHTTPConnector), errors.Is(err, store.ErrAgentRunNotCompletable), errors.Is(err, store.ErrInvalidAgent):
				writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			case errors.Is(err, httprunner.ErrInvalidEndpoint):
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			case errors.Is(err, httprunner.ErrRequestFailed), errors.Is(err, httprunner.ErrNon2xx), errors.Is(err, httprunner.ErrInvalidJSON):
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			default:
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to run http agent once"})
			}
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func runHTTPAgentInvocation(w http.ResponseWriter, r *http.Request, runtimeStore RuntimeStore, client *http.Client, runID string, req httpAgentInvokeBody, deps *httprunner.Deps) {
	result, err := httprunner.Invoke(r.Context(), runtimeStore, client, httprunner.InvokeInput{RunID: runID, Endpoint: req.Endpoint, Headers: req.Headers}, deps)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrUnknownAgentRun):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		case errors.Is(err, store.ErrInvalidHTTPConnector), errors.Is(err, store.ErrAgentRunNotCompletable), errors.Is(err, store.ErrInvalidAgent):
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		case errors.Is(err, httprunner.ErrInvalidEndpoint):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		case errors.Is(err, httprunner.ErrRequestFailed), errors.Is(err, httprunner.ErrNon2xx), errors.Is(err, httprunner.ErrInvalidJSON):
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		default:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to run http agent"})
		}
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// E2B sandbox smoke handler. One-off API key from request body or
// `E2B_API_KEY` env; sandbox is created, command runs, and (unless
// keep_alive=true) the sandbox is killed. Errors bubble up via
// `sanitizeE2BSmokeError` which redacts the API key.

// defaultDevOpenCodeSandboxTemplate is the E2B template id the dev
// smoke endpoints default to. `parsar-opencode-base` (infra/e2b-
// templates/opencode/) preinstalls the opencode CLI, Node 22, and the
// four ai-sdk provider adapters, avoiding a ~30s npm install round.
// Callers can override via `template`; non-dev callers set TemplateID
// on BuildSandboxRunnerOptions directly.
const defaultDevOpenCodeSandboxTemplate = "parsar-opencode-base"

type e2bSmokeRequest struct {
	APIKey                string            `json:"api_key"`
	APIBaseURL            string            `json:"api_base_url"`
	SandboxHost           string            `json:"sandbox_host"`
	SandboxBaseURL        string            `json:"sandbox_base_url"`
	Template              string            `json:"template"`
	Command               string            `json:"command"`
	TimeoutSeconds        int               `json:"timeout_seconds"`
	CommandTimeoutSeconds int               `json:"command_timeout_seconds"`
	KeepAlive             bool              `json:"keep_alive"`
	Env                   map[string]string `json:"env"`
}

type e2bSmokeResponse struct {
	SandboxID  string                   `json:"sandbox_id"`
	TemplateID string                   `json:"template_id"`
	Killed     bool                     `json:"killed"`
	Command    e2bsandbox.CommandResult `json:"command"`
}

func smokeE2BSandbox(w http.ResponseWriter, r *http.Request) {
	var req e2bSmokeRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
	}
	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("E2B_API_KEY"))
	}
	if apiKey == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "api_key or E2B_API_KEY is required"})
		return
	}
	template := strings.TrimSpace(req.Template)
	if template == "" {
		template = defaultDevOpenCodeSandboxTemplate
	}
	command := strings.TrimSpace(req.Command)
	if command == "" {
		command = `printf "hello from parsar e2b smoke\n"`
	}
	timeoutSeconds := req.TimeoutSeconds
	if timeoutSeconds <= 0 {
		timeoutSeconds = 120
	}
	commandTimeout := time.Duration(req.CommandTimeoutSeconds) * time.Second
	if commandTimeout <= 0 {
		commandTimeout = 60 * time.Second
	}
	secure := true
	client := &e2bsandbox.Client{
		HTTPClient:     http.DefaultClient,
		APIBaseURL:     req.APIBaseURL,
		SandboxHost:    req.SandboxHost,
		SandboxBaseURL: req.SandboxBaseURL,
		APIKey:         apiKey,
	}
	sandbox, err := client.Create(r.Context(), e2bsandbox.CreateInput{
		TemplateID:     template,
		TimeoutSeconds: timeoutSeconds,
		Secure:         &secure,
		Env:            req.Env,
		Metadata: map[string]string{
			"source": "parsar_dev_smoke",
		},
	})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": sanitizeE2BSmokeError(err, apiKey)})
		return
	}
	killed := false
	cleanupNeeded := !req.KeepAlive
	defer func() {
		if !cleanupNeeded {
			return
		}
		killCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := client.Kill(killCtx, sandbox.SandboxID); err == nil {
			killed = true
		}
	}()
	result, err := client.RunCommand(r.Context(), e2bsandbox.RunCommandInput{
		Sandbox: sandbox,
		Command: command,
		Timeout: commandTimeout,
	})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error":       sanitizeE2BSmokeError(err, apiKey),
			"sandbox_id":  sandbox.SandboxID,
			"template_id": sandbox.TemplateID,
			"keep_alive":  req.KeepAlive,
		})
		return
	}
	if !req.KeepAlive {
		killCtx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		killErr := client.Kill(killCtx, sandbox.SandboxID)
		cancel()
		killed = killErr == nil
		cleanupNeeded = false
	}
	writeJSON(w, http.StatusOK, e2bSmokeResponse{
		SandboxID:  sandbox.SandboxID,
		TemplateID: sandbox.TemplateID,
		Killed:     killed,
		Command:    result,
	})
}

func sanitizeE2BSmokeError(err error, apiKey string) string {
	if err == nil {
		return ""
	}
	return e2bsandbox.RedactSecret(err.Error(), apiKey)
}
