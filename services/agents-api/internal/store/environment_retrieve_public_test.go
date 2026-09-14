package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func TestEnvironmentRetrievalOfficialClient(t *testing.T) {
	python := os.Getenv("PARSAR_OFFICIAL_SDK_PYTHON")
	if python == "" {
		t.Skip("pinned official Python SDK required")
	}
	s, pool := store.NewTestStore(t)
	tenant, foreignTenant := uuid.NewString(), uuid.NewString()
	principal := store.FixtureExecutorPrincipal(t, s, tenant)
	token, peer, foreign := uuid.NewString(), uuid.NewString(), uuid.NewString()
	auth, err := api.NewAuthenticator([]api.APIKey{
		{OrganizationID: principal.OrganizationID, ProjectID: tenant, SubjectKind: principal.SubjectKind, SubjectID: principal.SubjectID, TokenSHA256: device.HashCredential(token), TenantID: tenant},
		{OrganizationID: principal.OrganizationID, ProjectID: tenant, SubjectKind: "user", SubjectID: "different-reader", TokenSHA256: device.HashCredential(peer), TenantID: tenant},
		{OrganizationID: principal.OrganizationID, ProjectID: foreignTenant, SubjectKind: principal.SubjectKind, SubjectID: principal.SubjectID, TokenSHA256: device.HashCredential(foreign), TenantID: foreignTenant},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureProjectScopes(t.Context(), auth.ProjectScopes()); err != nil {
		t.Fatal(err)
	}
	executor, err := s.IssueExecutorCredential(t.Context(), principal, uuid.NewString(), "")
	if err != nil {
		t.Fatal(err)
	}
	revoked := false
	defer func() {
		if !revoked {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := s.RevokeExecutorCredential(ctx, principal, executor.KeyID); err != nil {
				t.Error("owned executor credential cleanup failed", err)
			}
		}
	}()
	handler, err := api.NewHandler(s, auth, "codex", api.WithExecution(s), api.WithEnvironmentRemoteURL("https://private-registry.example"))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	settings := map[string]string{"base": server.URL, "token": token, "peer_token": peer, "foreign_token": foreign, "executor_token": executor.Token}
	run := func() map[string]string {
		t.Helper()
		input, err := json.Marshal(settings)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, python, "../../tests/official_environment_retrieve.py")
		command.Stdin = bytes.NewReader(input)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("official Environment retrieval: %v %s", err, output)
		}
		var result map[string]string
		if err := json.Unmarshal(output, &result); err != nil {
			t.Fatal("invalid official Environment retrieval result", err)
		}
		return result
	}
	result := run()
	before, err := s.GetEnvironment(t.Context(), tenant, result["environment_id"])
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeExecutorCredential(t.Context(), principal, executor.KeyID); err != nil {
		t.Fatal(err)
	}
	revoked = true
	server.Close()
	pool.Close()
	reopened, reopenedPool := store.NewTestStore(t)
	handler, err = api.NewHandler(reopened, auth, "codex")
	if err != nil {
		t.Fatal(err)
	}
	recovered := httptest.NewServer(handler)
	defer recovered.Close()
	settings["base"], settings["phase"] = recovered.URL, "reopened"
	for key, value := range result {
		settings[key] = value
	}
	run()
	after, err := reopened.GetEnvironment(t.Context(), tenant, before.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("public retrieval changed durable Environment state", err)
	}
	var history int
	if err := reopenedPool.QueryRow(t.Context(), `SELECT
		(SELECT count(*) FROM turns WHERE session_id=$1) +
		(SELECT count(*) FROM environment_input_reservations WHERE session_id=$1) +
		(SELECT count(*) FROM session_events WHERE session_id=$1)`, before.SessionID).Scan(&history); err != nil || history != 0 {
		t.Fatal("read-only retrieval admitted work or emitted events", history, err)
	}
}
