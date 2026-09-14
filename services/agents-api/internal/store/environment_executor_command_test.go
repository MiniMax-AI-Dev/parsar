package store_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func TestEnvironmentExecutorOperatorCommand(t *testing.T) {
	binary := os.Getenv("PARSAR_ENVIRONMENT_KEY_BINARY")
	if binary == "" {
		t.Skip("built environment-key operator executable required")
	}
	s, pool := store.NewTestStore(t)
	tenant := uuid.NewString()
	session, err := s.CreateSession(t.Context(), tenant, store.CreateSessionInput{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: "operator-key", Configuration: json.RawMessage(`{"environment":{"type":"self_hosted","workspace_directory":"/workspace"}}`)})
	if err != nil {
		t.Fatal(err)
	}
	environment, err := s.GetSessionEnvironment(t.Context(), tenant, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	command := func(owner string, success bool, flags ...string) string {
		t.Helper()
		args := append([]string{"--tenant", owner, "--environment", environment.ID}, flags...)
		cmd := exec.CommandContext(t.Context(), binary, args...)
		cmd.Env = append(os.Environ(), "AGENTS_API_DATABASE_URL="+pool.Config().ConnConfig.ConnString())
		data, err := cmd.Output()
		if (err == nil) != success {
			t.Fatal("unexpected operator outcome", flags)
		}
		if !success || (len(flags) == 1 && flags[0] == "--revoke") {
			if len(data) != 0 {
				t.Fatal("failed/revoke command emitted secret output")
			}
			return ""
		}
		var output struct {
			EnvironmentID string `json:"environment_id"`
			Token         string `json:"executor_token"`
		}
		if json.Unmarshal(data, &output) != nil || output.EnvironmentID != environment.ID || output.Token == "" {
			t.Fatal("invalid operator output")
		}
		return output.Token
	}
	command(uuid.NewString(), false)
	first := command(tenant, true)
	command(tenant, false)
	command(tenant, false, "--rotate", "--revoke")
	next := command(tenant, true, "--rotate")
	if next == first {
		t.Fatal("rotation returned the same key")
	}
	if _, err := s.AuthenticateEnvironmentExecutor(t.Context(), environment.ID, device.HashCredential(first)); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("old command credential retained authority", err)
	}
	if owner, err := s.AuthenticateEnvironmentExecutor(t.Context(), environment.ID, device.HashCredential(next)); err != nil || owner != tenant {
		t.Fatal("rotated command credential failed", err)
	}
	command(tenant, true, "--revoke")
	if _, err := s.AuthenticateEnvironmentExecutor(t.Context(), environment.ID, device.HashCredential(next)); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("revoked command credential retained authority", err)
	}
	t.Log("built operator command issued, rejected duplicate/foreign requests, rotated and revoked durable credentials")
}
