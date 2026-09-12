package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
)

func TestTextConfigurationHTTP(t *testing.T) {
	for _, tc := range []struct{ text, want string }{
		{``, "medium"}, {`,"text":null`, "medium"}, {`,"text":{}`, "medium"},
		{`,"text":{"verbosity":null,"format":null}`, "medium"},
		{`,"text":{"verbosity":"low"}`, "low"}, {`,"text":{"verbosity":"medium"}`, "medium"},
		{`,"text":{"verbosity":"high","format":{"type":"text"}}`, "high"},
	} {
		t.Run(tc.text, func(t *testing.T) {
			h, s, _ := testHandler(t)
			req := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions", strings.NewReader(`{"agent":{"model":"example"`+tc.text+`},"environment":{"type":"none"}}`))
			req.Header.Set("Authorization", "Bearer test-api-key")
			req.Header.Set("OpenAI-Beta", "agents=v1")
			response := httptest.NewRecorder()
			h.ServeHTTP(response, req)
			if response.Code != 200 {
				t.Fatal(response.Code, response.Body)
			}
			var got v1.Session
			if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			var saved configuration
			if err := json.Unmarshal(s.input.Configuration, &saved); err != nil {
				t.Fatal(err)
			}
			want := v1.TextConfig{Format: v1.TextFormat{Type: "text"}, Verbosity: tc.want}
			if got.Agent.Text != want || saved.Agent.Text != want {
				t.Fatal(got.Agent.Text, saved.Agent.Text)
			}
		})
	}
	for _, invalid := range []string{`{"verbosity":""}`, `{"verbosity":"verbose"}`, `{"verbosity":4}`, `{"format":{}}`, `{"format":{"type":"json_schema","schema":{}}}`, `{"format":{"type":"text","extra":true}}`, `{"unknown":true}`} {
		h, s, _ := testHandler(t)
		req := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions", strings.NewReader(`{"agent":{"model":"example","text":`+invalid+`},"environment":{"type":"none"}}`))
		req.Header.Set("Authorization", "Bearer test-api-key")
		req.Header.Set("OpenAI-Beta", "agents=v1")
		response := httptest.NewRecorder()
		h.ServeHTTP(response, req)
		if response.Code != 400 || s.tenant != "" {
			t.Fatalf("accepted invalid config %s: %d", invalid, response.Code)
		}
	}
}
