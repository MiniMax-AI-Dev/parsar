package store

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentskill"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/credentialcrypto"
	"github.com/google/uuid"
)

func TestSkillsEncryptedTemplateAndFrozenSession(t *testing.T) {
	_, pool := testStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{17}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithCredentialCipher(pool, cipher)
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	header := &zip.FileHeader{Name: "proof/SKILL.md", Method: zip.Store}
	file, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.Write([]byte("---\nname: proof\ndescription: A proof.\n---\nprivate-skill-canary")); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	setup := EnvironmentSetup{Skills: []InlineSkill{{Metadata: agentskill.Metadata{Type: "inline", Name: "proof", Description: "A proof."}, Archive: archive.Bytes()}}}
	tenant, foreign := uuid.NewString(), uuid.NewString()
	template, err := s.CreateEnvironmentTemplate(t.Context(), tenant, EnvironmentTemplateInput{SetSkills: true, Initialization: setup})
	if err != nil {
		t.Fatal(err)
	}
	public, err := New(pool).GetEnvironmentTemplate(t.Context(), tenant, template.ID)
	if err != nil || len(public.Skills) != 1 || !public.Initialization.Empty() {
		t.Fatal("public metadata", err)
	}
	var metadata, encrypted []byte
	if err = pool.QueryRow(t.Context(), "SELECT skills,skill_contents FROM environment_templates WHERE id=$1", template.ID).Scan(&metadata, &encrypted); err != nil || bytes.Contains(metadata, []byte("canary")) || bytes.Contains(encrypted, []byte("canary")) {
		t.Fatal("plaintext storage", err)
	}
	resolved, _, err := s.ResolveEnvironmentTemplate(t.Context(), tenant, template.ID)
	if err != nil || !reflect.DeepEqual(resolved.Initialization.Skills, setup.Skills) {
		t.Fatal("resolution", err)
	}
	if _, _, err = s.ResolveEnvironmentTemplate(t.Context(), foreign, template.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("tenant isolation", err)
	}
	session, err := s.CreateSession(t.Context(), tenant, CreateSessionInput{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: json.RawMessage(`{"environment":{"type":"openai_hosted"}}`), Initialization: resolved.Initialization})
	if err != nil {
		t.Fatal(err)
	}
	name := "renamed"
	if _, err = s.UpdateEnvironmentTemplate(t.Context(), tenant, template.ID, EnvironmentTemplateInput{SetName: true, Name: &name}); err != nil {
		t.Fatal(err)
	}
	preserved, _, err := s.ResolveEnvironmentTemplate(t.Context(), tenant, template.ID)
	if err != nil || !reflect.DeepEqual(preserved.Initialization.Skills, setup.Skills) {
		t.Fatal("unrelated update", err)
	}
	cleared, err := s.UpdateEnvironmentTemplate(t.Context(), tenant, template.ID, EnvironmentTemplateInput{SetSkills: true})
	if err != nil || len(cleared.Skills) != 0 {
		t.Fatal("clearing", err)
	}
	if _, err = s.DeleteEnvironmentTemplate(t.Context(), tenant, template.ID); err != nil {
		t.Fatal(err)
	}
	frozen, err := s.ReadEnvironmentSetup(t.Context(), tenant, session.ID)
	if err != nil || !reflect.DeepEqual(frozen.Skills, setup.Skills) {
		t.Fatal("frozen content changed", err)
	}
	if _, err = s.ReadEnvironmentSetup(t.Context(), foreign, session.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign content", err)
	}
}
