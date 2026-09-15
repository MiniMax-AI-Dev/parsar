package api

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestMCPCredentialReferenceIsSchemaNotAuthorization(t *testing.T) {
	for _, saved := range []bool{false, true} {
		input := strings.TrimSuffix(publicMCP, "}") + `,"credential_id":"unresolved-reference"}`
		raw, err := resolveMCPTool(json.RawMessage(input), saved)
		var output v1.MCPTool
		if err != nil || json.Unmarshal(raw, &output) != nil || output.CredentialID == nil || *output.CredentialID != "unresolved-reference" {
			t.Fatal("saved/effective schema resolved or lost the caller credential reference", err)
		}
	}
}

func TestSessionVaultTypesAndCreationIntent(t *testing.T) {
	for _, field := range []string{"", `,"vault_ids":null`, `,"vault_ids":[]`, `,"vault_ids":["vault"]`, `,"vault_ids":[null]`, `,"vault_ids":[3]`, `,"vault_ids":{}`, `,"vault_ids":"vault"`} {
		var decoded decodedSessionRequest
		err := json.Unmarshal([]byte(`{"agent":{"model":"model"},"environment":{"type":"none"}`+field+`}`), &decoded)
		if err != nil {
			t.Fatal(err)
		}
		input, err := decoded.validated()
		invalid := strings.Contains(field, "[null]") || strings.Contains(field, "[3]") || strings.Contains(field, "{}") || strings.Contains(field, `:"vault"`)
		if (err != nil) != invalid {
			t.Fatal("Vault IDs type/default decision differed", field)
		}
		if invalid {
			continue
		}
		intent, err := sessionCreationRequest(input, nil)
		if err != nil || (len(intent) != 0) != (len(input.VaultIDs) > 0) {
			t.Fatal("attached inline intent missing or unrelated inline identity changed", err)
		}
	}
	var request decodedSessionRequest
	body := `{"agent":{"model":"model","tools":[` + publicMCP + `]},"environment":{"type":"none"},"vault_ids":["vault"]}`
	if err := json.Unmarshal([]byte(body), &request); err != nil {
		t.Fatal(err)
	}
	input, _ := request.validated()
	first, _ := sessionCreationRequest(input, []store.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"original"}`)}})
	input.Stream = true
	second, _ := sessionCreationRequest(input, []store.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"original"}`)}})
	if !reflect.DeepEqual(first, second) {
		t.Fatal("streaming changed credential-bound creation identity")
	}
	input.VaultIDs = []string{"other"}
	changed, _ := sessionCreationRequest(input, []store.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"original"}`)}})
	if reflect.DeepEqual(first, changed) {
		t.Fatal("changed attachment reused caller intent")
	}
}

func TestSessionProjectionExcludesResolvedMCPSelection(t *testing.T) {
	var tool v1.MCPTool
	if json.Unmarshal([]byte(publicMCP), &tool) != nil {
		t.Fatal("invalid fixture")
	}
	encodedTool, _ := json.Marshal(tool)
	cfg := configuration{Agent: v1.Agent{ID: "agent", Model: "model", Tools: []json.RawMessage{encodedTool}}, Environment: v1.Environment{Type: "none"}, VaultIDs: []string{"attached"},
		MCPCredentials: []store.MCPCredentialBinding{{ServerLabel: "records", ServerURL: tool.Transport.ServerURL, VaultID: "attached", CredentialID: "private-selection", AuthType: "static_bearer"}}}
	raw, _ := json.Marshal(cfg)
	response, err := sessionResponse(store.Session{Configuration: raw}, "")
	if err != nil || !reflect.DeepEqual(response.VaultIDs, cfg.VaultIDs) {
		t.Fatal("public attachments lost", err)
	}
	public, _ := json.Marshal(response)
	if strings.Contains(string(public), "private-selection") || strings.Contains(string(public), "mcp_credentials") || !strings.Contains(string(public), `"credential_id":null`) {
		t.Fatal("public projection exposed implicit selection or changed caller reference")
	}
}
