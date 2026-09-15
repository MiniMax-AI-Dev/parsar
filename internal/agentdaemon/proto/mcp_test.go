package proto

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMCPHTTPBearerWireIsOptionalAndExact(t *testing.T) {
	empty, opaque := "", "synthetic-opaque-token"
	for _, token := range []*string{nil, &empty, &opaque} {
		input := MCPHTTPServer{ServerLabel: "tools", ServerURL: "https://tools.example/mcp", BearerToken: token}
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), `"bearer_token"`) != (token != nil) {
			t.Fatal("bearer field omission changed")
		}
		var decoded MCPHTTPServer
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatal(err)
		}
		if (decoded.BearerToken == nil) != (token == nil) || token != nil && *decoded.BearerToken != *token {
			t.Fatal("private token bytes or presence changed")
		}
	}
}
