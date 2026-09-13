package store_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func TestSavedReferenceRetryOfficialClient(t *testing.T) {
	python := os.Getenv("PARSAR_OFFICIAL_SDK_PYTHON")
	if python == "" {
		t.Skip("pinned official Python SDK required")
	}
	s, pool := store.NewTestStore(t)
	tenant, token, foreign := uuid.NewString(), uuid.NewString(), uuid.NewString()
	auth, err := api.NewAuthenticator([]api.APIKey{{TokenSHA256: device.HashCredential(token), TenantID: tenant}, {TokenSHA256: device.HashCredential(foreign), TenantID: uuid.NewString()}})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := execution.StartWorker(t.Context(), &execution.Dispatcher{Store: s})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := worker.Run(ctx); err != context.Canceled {
			t.Error(err)
		}
	})
	handler, err := api.NewHandler(s, auth, "codex", api.WithExecution(worker))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	recovered, err := api.NewHandler(store.New(pool), auth, "codex")
	if err != nil {
		t.Fatal(err)
	}
	restarted := httptest.NewServer(recovered)
	defer restarted.Close()
	// Source mutation is a controlled fixture until its public CRUD is implemented.
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			ID     string          `json:"id"`
			Delete bool            `json:"delete"`
			Patch  json.RawMessage `json:"patch"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "invalid fixture request", 400)
			return
		}
		var err error
		if input.Delete {
			_, err = pool.Exec(r.Context(), `DELETE FROM agents WHERE tenant_id=$1 AND id=$2`, tenant, input.ID)
		} else {
			_, err = pool.Exec(r.Context(), `UPDATE agents SET configuration=configuration || $3::jsonb WHERE tenant_id=$1 AND id=$2`, tenant, input.ID, input.Patch)
		}
		if err != nil {
			t.Error(err)
			http.Error(w, "fixture mutation failed", 500)
			return
		}
		w.WriteHeader(204)
	}))
	defer control.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "../../tests/official_agent_reference_retry.py", server.URL, token, foreign, control.URL, restarted.URL)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("official reference retry: %v %s", err, out)
	}
}
