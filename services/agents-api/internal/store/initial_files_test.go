package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/credentialcrypto"
	"github.com/google/uuid"
)

func TestInitialFilesFrozenEncryptedIsolatedAndRetryable(t *testing.T) {
	_, pool := testStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	s := NewWithCredentialCipher(pool, cipher)
	tenant, foreign := uuid.NewString(), uuid.NewString()
	canary := []byte("private-initial-file-canary\x00\xff")
	upload, err := s.CreateSourceFile(t.Context(), tenant, func(w io.Writer) (SourceFileUpload, error) {
		_, err := w.Write(canary)
		return SourceFileUpload{Filename: "source.bin", Purpose: "user_data"}, err
	})
	if err != nil {
		t.Fatal(err)
	}
	files := []InitialFile{{Type: "inline", Path: "/workspace/a/data", Data: canary}, {Type: "file_id", Path: "/workspace/b", FileID: upload.ID}}
	template, err := s.CreateEnvironmentTemplate(t.Context(), tenant, EnvironmentTemplateInput{SetFiles: true, Files: files})
	if err != nil {
		t.Fatal(err)
	}
	public, err := New(pool).GetEnvironmentTemplate(t.Context(), tenant, template.ID)
	if err != nil || len(public.Files) != 2 {
		t.Fatal("public read depends on secret key", err)
	}
	if _, _, err := s.ResolveEnvironmentTemplate(t.Context(), foreign, template.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign template resolved", err)
	}
	if _, err := s.UpdateEnvironmentTemplate(t.Context(), strings.ToUpper(tenant), strings.ToUpper(template.ID), EnvironmentTemplateInput{SetFiles: true, Files: files}); err != nil {
		t.Fatal("noncanonical update", err)
	}
	if _, _, err := s.ResolveEnvironmentTemplate(t.Context(), strings.ToUpper(tenant), strings.ToUpper(template.ID)); err != nil {
		t.Fatal("noncanonical resolution", err)
	}
	_, resolved, err := s.ResolveEnvironmentTemplate(t.Context(), tenant, template.ID)
	if err != nil || !bytes.Equal(resolved[0].Data, canary) {
		t.Fatal("template snapshot", err)
	}
	input := CreateSessionInput{Creator: FixtureCreator(), Engine: "codex", IdempotencyKey: uuid.NewString(), Configuration: json.RawMessage(`{"environment":{"type":"openai_hosted"}}`), InitialFiles: resolved}
	session, err := s.CreateSession(t.Context(), tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(session.Configuration, canary) || bytes.Contains(session.Configuration, []byte(`"data"`)) {
		t.Fatal("plaintext in Session configuration")
	}
	if _, err := s.UpdateEnvironmentTemplate(t.Context(), tenant, template.ID, EnvironmentTemplateInput{SetFiles: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteEnvironmentTemplate(t.Context(), tenant, template.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSourceFile(t.Context(), tenant, upload.ID); err != nil {
		t.Fatal(err)
	}
	retry, err := s.CreateSession(t.Context(), tenant, input)
	if err != nil || retry.ID != session.ID {
		t.Fatal("retry re-resolved deleted resources", err)
	}
	for position := range files {
		metadata, body, err := s.ReadInitialEnvironmentFile(t.Context(), strings.ToUpper(tenant), strings.ToUpper(session.ID), position)
		if err != nil || !bytes.Equal(body, canary) || metadata.ID == "" {
			t.Fatal("frozen initial content", err)
		}
		if _, _, err := s.ReadInitialEnvironmentFile(t.Context(), foreign, session.ID, position); err == nil {
			t.Fatal("foreign bytes disclosed")
		}
		var encrypted []byte
		if err := pool.QueryRow(t.Context(), "SELECT contents FROM initial_environment_files WHERE id=$1", metadata.ID).Scan(&encrypted); err != nil || bytes.Contains(encrypted, canary) {
			t.Fatal("unencrypted file storage", err)
		}
	}
	changed := input
	changed.InitialFiles = append([]InitialFile(nil), files...)
	changed.InitialFiles[0].Data = []byte("changed")
	if _, err := s.CreateSession(t.Context(), tenant, changed); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal("changed bytes retried", err)
	}
	if _, err := s.GetSessionDevice(t.Context(), tenant, session.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("uninitialized environment exposed", err)
	}
}
