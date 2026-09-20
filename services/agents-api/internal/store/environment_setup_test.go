package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/credentialcrypto"
	"github.com/google/uuid"
)

func TestEnvironmentSetupEncryptedSnapshotAndIsolation(t *testing.T) {
	_, pool := testStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{5}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithCredentialCipher(pool, cipher)
	tenant, foreign := uuid.NewString(), uuid.NewString()
	setup := EnvironmentSetup{Env: map[string]string{"SECRET": "template-env-canary"}, Commands: []SetupCommand{{Command: "printf template-command-canary > result"}}, Packages: v1.EnvironmentPackages{NPM: []string{"is-number@7.0.0"}, System: []string{"jq", "libpq-dev"}}}
	template, err := s.CreateEnvironmentTemplate(t.Context(), tenant, EnvironmentTemplateInput{Initialization: setup, SetEnv: true, SetSetup: true, SetPackages: true})
	if err != nil {
		t.Fatal(err)
	}
	public, err := New(pool).GetEnvironmentTemplate(t.Context(), tenant, template.ID)
	if err != nil || len(public.Packages.NPM) != 1 || !reflect.DeepEqual(public.Packages.System, setup.Packages.System) || !public.Initialization.Empty() {
		t.Fatal("public metadata requires plaintext or key", err)
	}
	resolved, _, err := s.ResolveEnvironmentTemplate(t.Context(), tenant, template.ID)
	if err != nil || !reflect.DeepEqual(resolved.Initialization.Env, setup.Env) || !reflect.DeepEqual(resolved.Initialization.Commands, setup.Commands) {
		t.Fatal("confidential configuration resolution", err)
	}
	if _, _, err := s.ResolveEnvironmentTemplate(t.Context(), foreign, template.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign resolution", err)
	}
	var envCipher, commandCipher []byte
	if err := pool.QueryRow(t.Context(), "SELECT env_contents, setup_contents FROM environment_templates WHERE id=$1", template.ID).Scan(&envCipher, &commandCipher); err != nil || bytes.Contains(envCipher, []byte("template-env-canary")) || bytes.Contains(commandCipher, []byte("template-command-canary")) {
		t.Fatal("plaintext template storage", err)
	}
	// Unrelated updates preserve both confidential fields; replacement is scoped.
	name := "renamed"
	if _, err := s.UpdateEnvironmentTemplate(t.Context(), tenant, template.ID, EnvironmentTemplateInput{SetName: true, Name: &name}); err != nil {
		t.Fatal(err)
	}
	retained, _, err := s.ResolveEnvironmentTemplate(t.Context(), tenant, template.ID)
	if err != nil || !reflect.DeepEqual(retained.Initialization, resolved.Initialization) {
		t.Fatal("name update changed initialization", err)
	}
	input := CreateSessionInput{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: json.RawMessage(`{"environment":{"type":"openai_hosted"}}`), Initialization: resolved.Initialization}
	session, err := s.CreateSession(t.Context(), tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(session.Configuration, []byte("canary")) {
		t.Fatal("plaintext Session metadata")
	}
	if _, err := s.UpdateEnvironmentTemplate(t.Context(), tenant, template.ID, EnvironmentTemplateInput{SetEnv: true}); err != nil {
		t.Fatal(err)
	}
	cleared, _, err := s.ResolveEnvironmentTemplate(t.Context(), tenant, template.ID)
	if err != nil || len(cleared.Initialization.Env) != 0 || !reflect.DeepEqual(cleared.Initialization.Commands, setup.Commands) {
		t.Fatal("field replacement lost unrelated values", err)
	}
	if _, err := s.DeleteEnvironmentTemplate(t.Context(), tenant, template.ID); err != nil {
		t.Fatal(err)
	}
	frozen, err := s.ReadEnvironmentSetup(t.Context(), tenant, session.ID)
	if err != nil || !reflect.DeepEqual(frozen, resolved.Initialization) {
		t.Fatal("Session did not freeze setup", err)
	}
	if _, err := s.ReadEnvironmentSetup(t.Context(), foreign, session.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign Session initialization", err)
	}
	retry, err := s.CreateSession(t.Context(), tenant, input)
	if err != nil || retry.ID != session.ID {
		t.Fatal("creation replay", err)
	}
	input.Initialization.Env = map[string]string{"SECRET": "changed"}
	if _, err := s.CreateSession(t.Context(), tenant, input); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal("changed setup retried", err)
	}
	if _, err := s.GetSessionExecutionBinding(t.Context(), tenant, session.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("uninitialized execution admitted", err)
	}
}
