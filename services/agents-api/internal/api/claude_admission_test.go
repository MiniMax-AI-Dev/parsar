package api

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestClaudeSessionConfigurationAdmission(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, initial := range []bool{false, true} {
			for _, test := range []struct {
				name, fields string
				accepted     bool
			}{
				{"defaults", `,"instructions":null`, true},
				{"medium", `,"text":{"verbosity":"medium"}`, true},
				{"low", `,"text":{"verbosity":"low"}`, false},
				{"high", `,"text":{"verbosity":"high"}`, false},
				{"object function", `,"tools":[{"type":"function","name":"lookup","description":"Look up a value","parameters":{"type":"object","properties":{}}}]`, true},
				{"implicit root", `,"tools":[{"type":"function","name":"lookup","description":"Look up a value","parameters":{"properties":{}}}]`, false},
				{"union root", `,"tools":[{"type":"function","name":"lookup","description":"Look up a value","parameters":{"type":["object","null"]}}]`, false},
				{"MCP defaults", `,"tools":[` + publicMCP + `]`, true},
				{"MCP null", `,"tools":[` + strings.TrimSuffix(publicMCP, "}") + `,"allowed_tools":null}]`, true},
				{"MCP empty", `,"tools":[` + strings.TrimSuffix(publicMCP, "}") + `,"allowed_tools":[]}]`, true},
				{"MCP selected", `,"tools":[` + strings.TrimSuffix(publicMCP, "}") + `,"allowed_tools":["lookup.v1"]}]`, true},
				{"MCP required", `,"tools":[` + strings.TrimSuffix(publicMCP, "}") + `,"required":true}]`, true},
				{"MCP reserved label", `,"tools":[` + strings.Replace(publicMCP, `"records"`, `"functions"`, 1) + `]`, false},
				{"MCP invalid label", `,"tools":[` + strings.Replace(publicMCP, `"records"`, `"records.v1"`, 1) + `]`, false},
				{"MCP wildcard name", `,"tools":[` + strings.TrimSuffix(publicMCP, "}") + `,"allowed_tools":["*"]}]`, false},
				{"MCP empty fragment", `,"tools":[` + strings.Replace(publicMCP, `/tools"`, `/tools#"`, 1) + `]`, false},
			} {
				t.Run(fmt.Sprintf("%s/stream=%t/initial=%t", test.name, stream, initial), func(t *testing.T) {
					digest := sha256.Sum256([]byte("test-api-key"))
					auth, err := NewAuthenticator([]APIKey{{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "test-runner", TokenSHA256: hex.EncodeToString(digest[:]), TenantID: uuid.NewString()}})
					if err != nil {
						t.Fatal(err)
					}
					saved := &recordingStore{}
					handler, err := NewHandler(saved, auth, "claude_sdk")
					if err != nil {
						t.Fatal(err)
					}
					input := ""
					if initial {
						input = `,"input":"Hello"`
					}
					body := fmt.Sprintf(`{"agent":{"model":"MiniMax-M3"%s},"environment":{"type":"none"},"stream":%t%s}`, test.fields, stream, input)
					request := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions", strings.NewReader(body))
					request.Header.Set("Authorization", "Bearer test-api-key")
					request.Header.Set("OpenAI-Beta", "agents=v1")
					response := httptest.NewRecorder()
					handler.ServeHTTP(response, request)
					want := http.StatusBadRequest
					if test.accepted {
						want = http.StatusOK
						if stream || initial {
							want = http.StatusServiceUnavailable
						}
					}
					if response.Code != want {
						t.Fatalf("status %d, expected %d: %s", response.Code, want, response.Body)
					}
					if want != http.StatusOK && saved.tenant != "" {
						t.Fatal("rejected request persisted a Session")
					}
					if want == http.StatusOK && saved.input.Engine != "claude_sdk" {
						t.Fatal("wrong engine persisted")
					}
				})
			}
		}
	}
}
