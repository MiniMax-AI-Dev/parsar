package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
)

func TestEnvironmentFilesReadOutlivesDefaultHTTPWriteDeadline(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			handler, fixture := environmentFilesHandler(t, true)
			fixture.readDelay = 100 * time.Millisecond
			if status == http.StatusServiceUnavailable {
				fixture.readError = execution.ErrExecutionUnavailable
			}
			server := httptest.NewUnstartedServer(handler)
			server.Config.WriteTimeout = 50 * time.Millisecond
			server.Start()
			defer server.Close()
			client := server.Client()
			client.Timeout = 5 * time.Second
			request, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
				server.URL+"/v1/agents/environments/"+fixture.environment.ID+"/files", nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer files-key")
			request.Header.Set("OpenAI-Beta", "agents=v1")
			response, err := client.Do(request)
			if err != nil {
				t.Fatal("read lost its response to the default write deadline", err)
			}
			defer response.Body.Close()
			var body map[string]json.RawMessage
			if err := json.NewDecoder(response.Body).Decode(&body); err != nil || response.StatusCode != status {
				t.Fatal("invalid delayed response", response.StatusCode, err)
			}
			if status == http.StatusOK {
				if string(body["data"]) != "[]" || string(body["next"]) != "null" {
					t.Fatal("delayed page changed shape")
				}
			} else if body["error"] == nil || body["data"] != nil || body["next"] != nil {
				t.Fatal("unavailable delayed read exposed a page")
			}
		})
	}
}
