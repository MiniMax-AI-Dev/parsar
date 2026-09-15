package api

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const publicMCP = `{"type":"mcp","server_label":"records","connection_origin":"service","transport":{"type":"http","server_url":"https://mcp.example.test/tools"}}`

func TestMCPResourceTransportProjections(t *testing.T) {
	for _, saved := range []bool{false, true} {
		raw, err := resolveMCPTool(json.RawMessage(publicMCP), saved)
		if err != nil {
			t.Fatal(err)
		}
		var actual map[string]any
		if err := json.Unmarshal(raw, &actual); err != nil {
			t.Fatal(err)
		}
		transport := map[string]any{"type": "http", "server_url": "https://mcp.example.test/tools"}
		if saved {
			transport["headers"] = map[string]any{}
		}
		expected := map[string]any{"type": "mcp", "server_label": "records", "transport": transport,
			"connection_origin": "service", "required": false, "allowed_tools": nil,
			"credential_id": nil, "request_metadata": map[string]any{}}
		if !reflect.DeepEqual(actual, expected) {
			t.Fatalf("saved=%v: unexpected pinned MCP resource: %s", saved, raw)
		}
	}
}

func TestMCPAllowedToolsAndOptionalFields(t *testing.T) {
	for _, allowed := range []string{"null", "[]", `["lookup","fail"]`} {
		var input map[string]json.RawMessage
		if err := json.Unmarshal([]byte(publicMCP), &input); err != nil {
			t.Fatal(err)
		}
		input["allowed_tools"] = json.RawMessage(allowed)
		input["credential_id"], input["request_metadata"], input["required"] = json.RawMessage("null"), json.RawMessage("null"), json.RawMessage("false")
		encoded, _ := json.Marshal(input)
		resolved, err := resolveMCPTool(encoded, false)
		if err != nil {
			t.Fatal(err)
		}
		var output map[string]json.RawMessage
		if err := json.Unmarshal(resolved, &output); err != nil || string(output["allowed_tools"]) != allowed {
			t.Fatalf("allow-list presence changed: %s, %v", resolved, err)
		}
	}
}

func TestMCPUnsupportedInputsAreSecretSafe(t *testing.T) {
	for name, replacement := range map[string]map[string]json.RawMessage{
		"origin missing":       {"connection_origin": nil},
		"origin null":          {"connection_origin": json.RawMessage("null")},
		"environment origin":   {"connection_origin": json.RawMessage(`"environment"`)},
		"required true":        {"required": json.RawMessage("true")},
		"required null":        {"required": json.RawMessage("null")},
		"empty credential":     {"credential_id": json.RawMessage(`""`)},
		"credential type":      {"credential_id": json.RawMessage(`3`)},
		"metadata":             {"request_metadata": json.RawMessage(`{"private-marker":"value"}`)},
		"null tool name":       {"allowed_tools": json.RawMessage(`[null]`)},
		"wrong allow-list":     {"allowed_tools": json.RawMessage(`"lookup"`)},
		"inline authorization": {"transport": json.RawMessage(`{"type":"http","server_url":"https://mcp.example.test","authorization":"private-marker"}`)},
		"headers":              {"transport": json.RawMessage(`{"type":"http","server_url":"https://mcp.example.test","headers":{"Authorization":"private-marker"}}`)},
		"URL credentials":      {"transport": json.RawMessage(`{"type":"http","server_url":"https://private-marker@mcp.example.test"}`)},
		"URL query":            {"transport": json.RawMessage(`{"type":"http","server_url":"https://mcp.example.test/?token=private-marker"}`)},
		"stdio":                {"transport": json.RawMessage(`{"type":"stdio","command":"private-marker"}`)},
	} {
		t.Run(name, func(t *testing.T) {
			var input map[string]json.RawMessage
			if err := json.Unmarshal([]byte(publicMCP), &input); err != nil {
				t.Fatal(err)
			}
			for key, value := range replacement {
				if value == nil {
					delete(input, key)
				} else {
					input[key] = value
				}
			}
			encoded, _ := json.Marshal(input)
			if _, err := resolveMCPTool(encoded, false); err == nil || strings.Contains(err.Error(), "private-marker") {
				t.Fatalf("unsupported MCP input was accepted or disclosed: %v", err)
			}
		})
	}
}

func TestSessionMCPKeepsToolOrderAndStripsSavedHeaders(t *testing.T) {
	saved, err := resolveMCPTool(json.RawMessage(publicMCP), true)
	if err != nil {
		t.Fatal(err)
	}
	function := json.RawMessage(`{"type":"function","name":"application","description":"An application callback","parameters":{"type":"object"},"defer_loading":false}`)
	tools, err := resolveSessionTools([]json.RawMessage{saved, function})
	if err != nil || len(tools) != 2 {
		t.Fatalf("mixed declaration: %v", err)
	}
	if strings.Contains(string(tools[0]), "headers") || string(tools[1]) != string(function) {
		t.Fatalf("Session transport or declared order changed: %s", tools)
	}
	if _, err := resolveSessionTools([]json.RawMessage{saved, function, saved}); err == nil {
		t.Fatal("duplicate MCP server labels were admitted")
	}
}
