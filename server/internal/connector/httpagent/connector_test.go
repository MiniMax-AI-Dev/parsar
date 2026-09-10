package httpagent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/httprunner"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/secrets"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type fakeStore struct {
	status string
	secret store.SecretPayload
	denied bool
}

func (s *fakeStore) GetAgentRun(context.Context, string) (store.AgentRunDetailRead, error) {
	return store.AgentRunDetailRead{AgentRunBriefRead: store.AgentRunBriefRead{Status: s.status}}, nil
}
func (s *fakeStore) GetHTTPAgentSecretPayload(context.Context, string, string) (store.SecretPayload, error) {
	if s.denied {
		return store.SecretPayload{}, errors.New("denied")
	}
	return s.secret, nil
}
func TestStreamReturnsOneReplyWithUsageAndKeepsCredentialsOutOfBody(t *testing.T) {
	vault, _ := secrets.New("synthetic-test-key")
	encrypted, _ := vault.Encrypt(map[string]any{"token": "synthetic-test-token"})
	st := &fakeStore{status: "running", secret: store.SecretPayload{EncryptedPayload: encrypted}}
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-test-token" {
			t.Error("missing bearer header")
		}
		var request httprunner.AgentRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if len(request.AgentConfig) != 1 || request.AgentConfig["system_prompt"] != "instructions" || request.TriggerMessageContent != "hello" || request.ConversationID != "conversation" {
			t.Errorf("unexpected public request: %+v", request)
		}
		json.NewEncoder(w).Encode(httprunner.AgentResponse{Content: "one reply", Usage: store.UsageInput{Provider: "test", Model: "test-model", InputTokens: 10, OutputTokens: 2}})
	}))
	defer endpoint.Close()
	c := New(st, "synthetic-test-key", nil)
	events, err := c.StreamPrompt(context.Background(), connector.PromptInput{RunID: "run", ConversationID: "conversation", TriggerMessageContent: "hello", AgentConfig: map[string]any{"http": map[string]any{"endpoint": endpoint.URL, "secret_id": "secret"}, "system_prompt": "instructions", "credential_bindings": map[string]any{"secret": "private"}}})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for event := range events {
		count++
		if event.Type != connector.EventDone || event.Final == nil || event.Final.Content != "one reply" || event.Final.Usage.InputTokens != 10 {
			t.Fatalf("unexpected event: %+v", event)
		}
	}
	if count != 1 {
		t.Fatalf("expected one final reply, got %d events", count)
	}
}
func TestCancelAbortsOnlyMatchingRun(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
		close(stopped)
	}))
	defer endpoint.Close()
	c := New(&fakeStore{status: "running"}, "", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	events, err := c.StreamPrompt(ctx, connector.PromptInput{RunID: "run", ConversationID: "conversation", AgentConfig: map[string]any{"endpoint": endpoint.URL}})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	c.Abort(ctx, connector.AbortInput{RunID: "run", ConversationID: "other"})
	select {
	case <-events:
		t.Fatal("wrong conversation cancelled run")
	default:
	}
	c.Abort(ctx, connector.AbortInput{RunID: "run", ConversationID: "conversation"})
	select {
	case <-stopped:
	case <-ctx.Done():
		t.Fatal("HTTP request was not cancelled")
	}
	first, last := <-events, <-events
	if first.Type != connector.EventError || last.Type != connector.EventDone || last.Final != nil {
		t.Fatal("cancellation did not finish as an error")
	}
}
func TestCancelledRunAndUnavailableSecretNeverReachEndpoint(t *testing.T) {
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected outbound request") }))
	defer endpoint.Close()
	for _, st := range []*fakeStore{{status: "cancelled"}, {status: "running", denied: true}} {
		c := New(st, "synthetic-test-key", nil)
		_, err := c.Prompt(context.Background(), connector.PromptInput{RunID: "run", AgentConfig: map[string]any{"http": map[string]any{"endpoint": endpoint.URL, "secret_id": "secret"}}})
		if err == nil {
			t.Fatal("expected rejection")
		}
	}
}
