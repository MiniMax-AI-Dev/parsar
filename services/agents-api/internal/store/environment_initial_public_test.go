package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func TestEnvironmentInitialFailureOfficialClient(t *testing.T) {
	python := os.Getenv("PARSAR_OFFICIAL_SDK_PYTHON")
	if python == "" {
		t.Skip("pinned official Python SDK required")
	}
	s, pool := store.NewTestStore(t)
	tenant, token, foreign := uuid.NewString(), uuid.NewString(), uuid.NewString()
	auth, err := api.NewAuthenticator([]api.APIKey{
		{OrganizationID: "test-org", ProjectID: tenant, SubjectKind: "service_account", SubjectID: "test-runner", TokenSHA256: device.HashCredential(token), TenantID: tenant},
		{OrganizationID: "test-org", ProjectID: "other-project", SubjectKind: "service_account", SubjectID: "test-runner", TokenSHA256: device.HashCredential(foreign), TenantID: uuid.NewString()},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Private setup isolates the persistence prerequisite from public creation admission.
	configuration := json.RawMessage(`{"agent":{"id":"agent_initial_failure","model":"fixture","tools":[],"multi_agent":{"enabled":false,"max_concurrent_subagents":null},"reasoning":{},"service_tier":"auto","text":{"format":{"type":"text"},"verbosity":"medium"}},"environment":{"type":"self_hosted","workspace_directory":"/workspace"}}`)
	session, err := s.CreateSession(t.Context(), tenant, store.CreateSessionInput{
		Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: "initial", Configuration: configuration,
		InitialInputs: []store.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"private-input-marker"}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := s.AcquireExecutionLease(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := lease.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	handler, err := api.NewHandler(s, auth, "codex", api.WithEnvironmentRemoteURL("https://executor.example"))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	directory := t.TempDir()
	settings, err := json.Marshal(map[string]string{"base": server.URL, "token": token, "foreign_token": foreign, "session_id": session.ID, "environment_id": session.Environment.ID, "directory": directory})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	command := exec.CommandContext(ctx, python, "../../tests/official_environment_initial_failure.py")
	command.Stdin = bytes.NewReader(settings)
	type result struct {
		output []byte
		err    error
	}
	done := make(chan result, 1)
	go func() { output, err := command.CombinedOutput(); done <- result{output, err} }()
	waited := false
	defer func() {
		cancel()
		if !waited {
			<-done
		}
	}()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		_, sdkErr := os.Stat(filepath.Join(directory, "sdk-ready"))
		_, rawErr := os.Stat(filepath.Join(directory, "raw-ready"))
		if sdkErr == nil && rawErr == nil {
			break
		}
		select {
		case result := <-done:
			waited = true
			t.Fatalf("official initial failure observer stopped before readiness: %v %s", result.err, result.output)
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("official initial failure observers did not become ready")
		}
	}
	var reservation string
	if err := pool.QueryRow(t.Context(), "UPDATE environment_input_reservations SET deadline=clock_timestamp()-interval '1 second' WHERE session_id=$1 AND is_initial RETURNING id", session.ID).Scan(&reservation); err != nil {
		t.Fatal(err)
	}
	if result, err := lease.Store().ExpireEnvironmentInput(t.Context(), tenant, session.ID, reservation); err != nil || result.State != store.EnvironmentInputExpired {
		t.Fatal("initial reservation did not expire", result, err)
	}
	observed := <-done
	waited = true
	if observed.err != nil {
		t.Fatalf("official initial failure: %v %s", observed.err, observed.output)
	}
	t.Log(string(observed.output))
}
