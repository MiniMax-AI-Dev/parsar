package store

import (
	"bytes"
	"encoding/json"
	"errors"
	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/credentialcrypto"
	"github.com/google/uuid"
	"testing"
)

func TestSessionModelExecutionEncryptedAndBound(t *testing.T) {
	_, pool := testStore(t)
	cipher, err := credentialcrypto.New(bytes.Repeat([]byte{17}, 32))
	if err != nil {
		t.Fatal(err)
	}
	st := NewWithCredentialCipher(pool, cipher)
	ctx := t.Context()
	tenant := uuid.NewString()
	input := CreateSessionInput{Creator: FixtureCreator(), Engine: "mcode", IdempotencyKey: uuid.NewString(), Configuration: json.RawMessage(`{"agent":{"model":"actual-model"},"environment":{"type":"openai_hosted"}}`), ModelProvider: &v1.ModelProviderInput{Protocol: "anthropic", BaseURL: "https://example.com", APIKey: "private-model-canary", ContextWindow: 100000, MaxOutputTokens: 8000}}
	session, err := st.CreateSession(ctx, tenant, input)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(session.Configuration, []byte("private-model-canary")) || !bytes.Contains(session.Configuration, []byte(`"model_provider_configured":true`)) {
		t.Fatal("unsafe or missing configuration marker")
	}
	var ciphertext []byte
	if err := pool.QueryRow(ctx, "SELECT encrypted_config FROM session_model_execution WHERE session_id=$1", session.ID).Scan(&ciphertext); err != nil || bytes.Contains(ciphertext, []byte("private-model-canary")) {
		t.Fatal("plaintext key in storage", err)
	}
	replay, err := st.CreateSession(ctx, tenant, input)
	if err != nil || replay.ID != session.ID {
		t.Fatal("retry changed snapshot", err)
	}
	input.ModelProvider.APIKey = "conflicting-key"
	if _, err := st.CreateSession(ctx, tenant, input); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal("changed credentials accepted", err)
	}
	restarted := NewWithCredentialCipher(pool, cipher)
	provider, err := restarted.SessionModelExecution(ctx, tenant, session.ID)
	if err != nil || provider.APIKey != "private-model-canary" {
		t.Fatal("restart lost model credential", err)
	}
	if _, err := restarted.SessionModelExecution(ctx, uuid.NewString(), session.ID); err == nil {
		t.Fatal("foreign tenant read credential")
	}
	if _, err := cipher.OpenModelExecution(ciphertext, tenant, uuid.NewString()); err == nil {
		t.Fatal("ciphertext not Session-bound")
	}
	if _, err := New(pool).SessionModelExecution(ctx, tenant, session.ID); err == nil {
		t.Fatal("missing cipher succeeded")
	}
	input.IdempotencyKey = uuid.NewString()
	if _, err := New(pool).CreateSession(ctx, tenant, input); !errors.Is(err, ErrCredentialStorageUnavailable) {
		t.Fatal("unencrypted create", err)
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM sessions WHERE tenant_id=$1 AND idempotency_key=$2", tenant, input.IdempotencyKey).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed creation left partial Session", err)
	}
}
