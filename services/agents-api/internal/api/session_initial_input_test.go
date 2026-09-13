package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInitialInputUsesExecutionAdmissionAndSharedMessageValidation(t *testing.T) {
	for _, value := range []string{`"First"`, `[{"role":"user","content":[{"type":"input_text","text":"First"}]}]`} {
		execution := &recordingStore{}
		recorder := &inputRecorder{ResourceStore: execution}
		h, idle, tenant := testHandler(t, WithExecution(recorder))
		r := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions", strings.NewReader(`{"agent":{"model":"test-model"},"environment":{"type":"none"},"input":`+value+`}`))
		r.Header.Set("Authorization", "Bearer test-api-key")
		r.Header.Set("OpenAI-Beta", "agents=v1")
		r.Header.Set("Idempotency-Key", "create-key")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 || idle.tenant != "" || execution.tenant != tenant || execution.input.IdempotencyKey != "create-key" || len(execution.input.InitialInputs) != 1 || recorder.inputs != nil {
			t.Fatal(w.Code, w.Body, execution.input)
		}
		expected, err := executionInputs([]json.RawMessage{json.RawMessage(`{"type":"agent.session.input.message","input":[{"role":"user","content":[{"type":"input_text","text":"First"}]}]}`)})
		if err != nil || string(expected[0].Payload) != string(execution.input.InitialInputs[0].Payload) {
			t.Fatal(execution.input, err)
		}
	}
	for _, value := range []string{`0`, `true`, `{}`, `[]`, `" "`, `[{"role":"user","unknown":true,"content":[{"type":"input_text","text":"x"}]}]`, `[{"role":"user","content":[{"type":"input_text","text":"x","unknown":true}]}]`, `[{"role":"assistant","content":[{"type":"input_text","text":"x"}]}]`} {
		if _, err := initialSessionInputs(json.RawMessage(value)); err == nil {
			t.Fatal("invalid initial input accepted", value)
		}
	}
	for _, value := range []json.RawMessage{nil, json.RawMessage(` null `)} {
		if got, err := initialSessionInputs(value); err != nil || got != nil {
			t.Fatal(got, err)
		}
	}
}
