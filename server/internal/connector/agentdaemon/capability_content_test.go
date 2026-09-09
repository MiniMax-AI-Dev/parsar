package agentdaemon

import (
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

func TestCapabilityContentVersionSelection(t *testing.T) {
	for _, kind := range []string{"mcp", "legacy_mcp", "system_prompt"} {
		for _, mode := range []string{"", store.PinningModePinned, store.PinningModeLatest} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				row := store.EnabledCapabilityRead{CapabilityID: "cap", Name: "versioned", Type: "mcp", PinningMode: mode}
				if kind == "system_prompt" {
					row.Type = kind
					row.CanonicalSpec = []byte(`{"schema_version":1,"kind":"system_prompt","system_prompt":{"prompt":"v1","mode":"append"}}`)
					row.LatestCanonicalSpec = []byte(`{"schema_version":1,"kind":"system_prompt","system_prompt":{"prompt":"v2","mode":"override"}}`)
				} else if kind == "legacy_mcp" {
					row.Content = []byte(`{"mcpServers":{"versioned":{"command":"v1"}}}`)
					row.LatestContent = []byte(`{"mcpServers":{"versioned":{"command":"v2"}}}`)
				} else {
					row.CanonicalSpec = newMCPRow(t, "cap", "versioned", []canonical.MCPServer{{Name: "versioned", Command: "v1"}}, nil).CanonicalSpec
					row.LatestCanonicalSpec = newMCPRow(t, "cap", "versioned", []canonical.MCPServer{{Name: "versioned", Command: "v2"}}, nil).CanonicalSpec
				}
				c := &Connector{capabilities: stubCapabilityStore{rows: []store.EnabledCapabilityRead{row}}, log: discardLogger()}
				got, err := c.resolveCapabilityAdditions(t.Context(), defaultPromptInput(), "claude_code")
				if err != nil {
					t.Fatal(err)
				}
				want := "v1"
				if mode == store.PinningModeLatest {
					want = "v2"
				}
				if kind == "system_prompt" {
					if len(got.SystemPrompts) != 1 || got.SystemPrompts[0].Content != want {
						t.Fatalf("system prompt = %+v, want %s", got.SystemPrompts, want)
					}
					wantMode := canonical.SystemPromptModeAppend
					if mode == store.PinningModeLatest {
						wantMode = canonical.SystemPromptModeOverride
					}
					if got.SystemPrompts[0].Mode != wantMode {
						t.Fatalf("prompt mode = %s, want %s", got.SystemPrompts[0].Mode, wantMode)
					}
				} else if server, ok := got.MCPServers["versioned"].(map[string]any); !ok || server["command"] != want {
					t.Fatalf("MCP servers = %+v, want %s", got.MCPServers, want)
				}
			})
		}
	}
}

func TestMCPLatestVersionCredentials(t *testing.T) {
	svc := testSecretsService(t)
	v2 := newMCPRow(t, "cap", "versioned", []canonical.MCPServer{{Name: "versioned", Command: "echo", Env: map[string]canonical.EnvValue{
		"TOKEN": {Mode: canonical.EnvModeCredentialRef, CredentialKindCode: "new_token"},
	}}}, []store.RequiredCredential{{Kind: "new_token", Required: true}})
	row := newMCPRow(t, "cap", "versioned", []canonical.MCPServer{{Name: "old", Command: "echo"}}, []store.RequiredCredential{{Kind: "old_token", Required: true}})
	row.PinningMode, row.CapabilityVersionID, row.LatestVersionID = store.PinningModeLatest, "v1", "v2"
	row.LatestCanonicalSpec, row.LatestRequiredCredentials = v2.CanonicalSpec, v2.RequiredCredentials
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "available", true: "missing"}[missing], func(t *testing.T) {
			creds := map[string]store.UserCredentialRead{}
			if !missing {
				creds["user-1:new_token"] = store.UserCredentialRead{ID: "credential-new", UserID: "user-1", Kind: "new_token", Ciphertext: encryptPayload(t, svc, map[string]any{"token": "synthetic-token"})}
			}
			c := &Connector{capabilities: stubCapabilityStore{rows: []store.EnabledCapabilityRead{row}, credentials: creds}, secrets: svc, log: discardLogger()}
			got, err := c.resolveCapabilityAdditions(t.Context(), defaultPromptInput(), "claude_code")
			if err != nil {
				t.Fatal(err)
			}
			if missing {
				if len(got.MCPServers) != 0 || len(got.Disabled) != 1 || len(got.Disabled[0].MissingCredentials) != 1 || got.Disabled[0].CapabilityVersionID != "v2" || got.Disabled[0].MissingCredentials[0].Kind != "new_token" {
					t.Fatalf("missing credential result = %+v", got)
				}
				return
			}
			server, ok := got.MCPServers["versioned"].(map[string]any)
			if !ok {
				t.Fatalf("MCP server missing: %+v", got.MCPServers)
			}
			env, ok := server["env"].(map[string]string)
			if !ok || env["TOKEN"] != "synthetic-token" {
				t.Fatalf("new credential was not resolved")
			}
			if len(got.CredentialEmits) != 1 || got.CredentialEmits[0].CapabilityVersionID != "v2" || got.CredentialEmits[0].CredentialKind != "new_token" {
				t.Fatalf("credential audit = %+v", got.CredentialEmits)
			}
		})
	}
}

func TestMCPLatestPublicCredentials(t *testing.T) {
	svc := testSecretsService(t)
	v2 := newMCPRow(t, "cap", "versioned", []canonical.MCPServer{{Name: "versioned", Command: "echo", Env: map[string]canonical.EnvValue{
		"TOKEN": {Mode: canonical.EnvModeCredentialRef, CredentialKindCode: "github_pat"},
	}}}, []store.RequiredCredential{{Kind: "github_pat", Required: true}})
	for _, tc := range []struct {
		name, visibility, mode, token string
		shared, disabled              bool
	}{
		{name: "workspace latest", visibility: "workspace", mode: store.PinningModeLatest, token: "private-token"},
		{name: "public latest unbound", visibility: "public", mode: store.PinningModeLatest, disabled: true},
		{name: "public latest shared", visibility: "public", mode: store.PinningModeLatest, shared: true, token: "sk-shared"},
		{name: "public pinned", visibility: "public", mode: store.PinningModePinned},
		{name: "public omitted mode", visibility: "public"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := newMCPRow(t, "cap", "versioned", []canonical.MCPServer{{Name: "versioned", Command: "echo"}}, nil)
			row.AgentVisibility, row.PinningMode = tc.visibility, tc.mode
			row.CapabilityVersionID, row.LatestVersionID = "v1", "v2"
			row.LatestCanonicalSpec, row.LatestRequiredCredentials = v2.CanonicalSpec, v2.RequiredCredentials
			if tc.shared {
				row.Configuration = map[string]any{"credential_bindings": map[string]any{"github_pat": map[string]any{"source": "shared", "secret_id": "secret-shared"}}}
			}
			c := &Connector{capabilities: stubCapabilityStore{rows: []store.EnabledCapabilityRead{row}, credentials: map[string]store.UserCredentialRead{
				"user-1:github_pat": {ID: "personal", UserID: "user-1", Kind: "github_pat", Ciphertext: encryptPayload(t, svc, map[string]any{"token": "private-token"})},
			}}, secrets: svc, modelResolver: sharedBindingResolver(t, svc), log: discardLogger()}
			got, err := c.resolveCapabilityAdditions(t.Context(), defaultPromptInput(), "claude_code")
			if err != nil {
				t.Fatal(err)
			}
			if tc.disabled {
				if len(got.MCPServers) != 0 || len(got.CredentialEmits) != 0 || len(got.Disabled) != 1 || got.Disabled[0].CapabilityVersionID != "v2" {
					t.Fatalf("public MCP with an unbound required credential was not disabled: %+v", got)
				}
				return
			}
			if len(got.Disabled) != 0 || len(got.MCPServers) != 1 {
				t.Fatalf("expected available MCP: %+v", got)
			}
			if tc.token == "" {
				if len(got.CredentialEmits) != 0 {
					t.Fatal("credential-free stored version emitted a credential")
				}
				return
			}
			server := got.MCPServers["versioned"].(map[string]any)
			if server["env"].(map[string]string)["TOKEN"] != tc.token || len(got.CredentialEmits) != 1 || got.CredentialEmits[0].CapabilityVersionID != "v2" {
				t.Fatal("selected version credential or audit did not match")
			}
		})
	}
}
