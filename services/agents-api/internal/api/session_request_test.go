package api

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
)

func TestSessionCreateFieldPresence(t *testing.T) {
	// Pinned SessionCreateParams: stream and agent_id are not nullable;
	// metadata is nullable, but its values must be strings.
	for _, tc := range []struct {
		name, fields string
		status       int
		metadata     map[string]string
	}{
		{"omitted", ``, 200, nil},
		{"false stream", `,"stream":false`, 200, nil},
		{"null stream", `,"stream":null`, 400, nil},
		{"whitespace null stream", `,"stream": null `, 400, nil},
		{"string stream", `,"stream":"false"`, 400, nil},
		{"numeric stream", `,"stream":0`, 400, nil},
		{"null agent ID", `,"agent_id":null`, 400, nil},
		{"numeric agent ID", `,"agent_id":0`, 400, nil},
		{"null metadata", `,"metadata":null`, 200, nil},
		{"empty metadata", `,"metadata":{}`, 200, nil},
		{"string metadata", `,"metadata":{"empty":"","label":"中文🧪"}`, 200, map[string]string{"empty": "", "label": "中文🧪"}},
		{"null metadata value", `,"metadata":{"label":null}`, 400, nil},
		{"mixed metadata values", `,"metadata":{"empty":"","label":null}`, 400, nil},
		{"numeric metadata value", `,"metadata":{"label":0}`, 400, nil},
		{"array metadata", `,"metadata":[]`, 400, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler, saved, _ := testHandler(t)
			body := `{"agent":{"model":"example"},"environment":{"type":"none"}` + tc.fields + `}`
			request := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions", strings.NewReader(body))
			request.Header.Set("Authorization", "Bearer test-api-key")
			request.Header.Set("OpenAI-Beta", "agents=v1")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, tc.status, response.Body)
			}
			if tc.status != http.StatusOK {
				if saved.tenant != "" {
					t.Fatal("invalid request reached persistence")
				}
				var failure v1.ErrorResponse
				if json.Unmarshal(response.Body.Bytes(), &failure) != nil || failure.Error.Code != "invalid_request" {
					t.Fatalf("invalid error response: %s", response.Body)
				}
				return
			}
			if saved.tenant == "" || !maps.Equal(saved.input.Metadata, tc.metadata) {
				t.Fatalf("saved metadata = %#v, want %#v", saved.input.Metadata, tc.metadata)
			}
			var session v1.Session
			if json.Unmarshal(response.Body.Bytes(), &session) != nil || !maps.Equal(session.Metadata, tc.metadata) {
				t.Fatalf("metadata response changed: %s", response.Body)
			}
		})
	}
}
