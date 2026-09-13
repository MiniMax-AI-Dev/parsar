package store_test

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func TestAgentUpdateOfficialClient(t *testing.T) {
	python := os.Getenv("PARSAR_OFFICIAL_SDK_PYTHON")
	if python == "" {
		t.Skip("pinned official Python SDK required")
	}
	s, pool := store.NewTestStore(t)
	token, foreign := uuid.NewString(), uuid.NewString()
	auth, err := api.NewAuthenticator([]api.APIKey{
		{TokenSHA256: device.HashCredential(token), TenantID: uuid.NewString()},
		{TokenSHA256: device.HashCredential(foreign), TenantID: uuid.NewString()},
	})
	if err != nil {
		t.Fatal(err)
	}
	h, err := api.NewHandler(s, auth, "codex")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	h, err = api.NewHandler(store.New(pool), auth, "codex")
	if err != nil {
		t.Fatal(err)
	}
	recovered := httptest.NewServer(h)
	defer recovered.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "../../tests/official_agent_update.py", server.URL, token, foreign, recovered.URL)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("official Agent update: %v %s", err, out)
	}
	t.Log(string(out))
}
