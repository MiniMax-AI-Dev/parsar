package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

type inputRecorder struct {
	tenant, session, key string
	inputs               []store.Input
	err                  error
}

func (s *inputRecorder) SubmitInputs(_ context.Context, tenant, session, key string, inputs []store.Input) ([]store.InputReceipt, error) {
	s.tenant, s.session, s.key, s.inputs = tenant, session, key, inputs
	return nil, s.err
}

func TestPublicInputAdmission(t *testing.T) {
	recorder := &inputRecorder{}
	h, _, tenant := testHandler(t, WithExecution(recorder))
	body := `{"events":[{"type":"agent.session.input.message","input":[{"role":"user","content":[{"type":"input_text","text":"First"}]},{"role":"user","content":[{"type":"input_text","text":"Second"}]}]},{"type":"agent.session.input.cancel"}]}`
	r := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions/session-id/events", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer test-api-key")
	r.Header.Set("OpenAI-Beta", "agents=v1")
	r.Header.Set("Idempotency-Key", "batch-key")
	r.Header.Set("X-Tenant-ID", "forged")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 204 || w.Body.Len() != 0 || recorder.tenant != tenant || recorder.session != "session-id" || recorder.key != "batch-key" {
		t.Fatalf("response=%d %s recorder=%+v", w.Code, w.Body, recorder)
	}
	if len(recorder.inputs) != 2 || recorder.inputs[0].Kind != "message" || recorder.inputs[1].Kind != "cancel" {
		t.Fatal(recorder.inputs)
	}
	var stored map[string]json.RawMessage
	if err := json.Unmarshal(recorder.inputs[0].Payload, &stored); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stored["input"]), "Second") {
		t.Fatal("individual user messages lost")
	}
}

func TestPublicInputRejectsUnsupportedOrMalformedBatch(t *testing.T) {
	for _, body := range []string{
		`null`, `{}`, `{"events":[]}`, `{"events":[{"type":"agent.session.input.tool_result","call_id":"x"}]}`,
		`{"events":[{"type":"agent.session.input.message","input":[{"role":"assistant","content":[{"type":"input_text","text":"x"}]}]}]}`,
		`{"events":[{"type":"agent.session.input.message","input":[{"role":"user","content":[{"type":"input_image","image_url":"https://example.com/a.png"}]}]}]}`,
		`{"events":[{"type":"agent.session.input.cancel","input":[]}]}`,
		`{"events":[{"type":"agent.session.input.message","input":[{"role":"user","content":[{"type":"input_text","text":" "}]}]}]}`,
		`{"events":[{"type":"agent.session.input.cancel"}]} {}`,
	} {
		recorder := &inputRecorder{}
		h, _, _ := testHandler(t, WithExecution(recorder))
		r := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions/id/events", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer test-api-key")
		r.Header.Set("OpenAI-Beta", "agents=v1")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 || recorder.inputs != nil {
			t.Fatalf("accepted %s: %d %s", body, w.Code, w.Body)
		}
	}
}
