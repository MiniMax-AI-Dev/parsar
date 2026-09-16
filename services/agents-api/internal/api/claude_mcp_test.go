package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

type claudeCredentialStore struct {
	recordingStore
	binding store.MCPCredentialBinding
	calls   int
}

func (s *claudeCredentialStore) ResolveMCPCredentials(_ context.Context, _ string, _ []string, _ []store.MCPCredentialRequest) ([]store.MCPCredentialBinding, error) {
	s.calls++
	return []store.MCPCredentialBinding{s.binding}, nil
}

func TestClaudeMCPAdmitsResolvedCredentials(t *testing.T) {
	for _, selection := range []string{"implicit", "explicit", "unmatched"} {
		t.Run(selection, func(t *testing.T) {
			vault, credential := uuid.NewString(), uuid.NewString()
			s := &claudeCredentialStore{binding: store.MCPCredentialBinding{ServerLabel: "records", ServerURL: "https://mcp.example.test/tools", VaultID: vault, CredentialID: credential, AuthType: "static_bearer"}}
			tool := publicMCP
			if selection == "explicit" {
				tool = strings.TrimSuffix(tool, "}") + `,"credential_id":"` + credential + `"}`
			}
			if selection == "unmatched" {
				s.binding.VaultID, s.binding.CredentialID, s.binding.AuthType = "", "", ""
			}
			digest := sha256.Sum256([]byte("test-api-key"))
			auth, err := NewAuthenticator([]APIKey{{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "test-runner", TokenSHA256: hex.EncodeToString(digest[:]), TenantID: uuid.NewString()}})
			if err != nil {
				t.Fatal(err)
			}
			h, err := NewHandler(s, auth, "claude_sdk")
			if err != nil {
				t.Fatal(err)
			}
			body := fmt.Sprintf(`{"agent":{"model":"model","tools":[%s]},"environment":{"type":"none"},"vault_ids":[%q]}`, tool, vault)
			response := credentialRequest(h, "POST", "/v1/agents/sessions", body)
			if response.Code != 200 || s.calls != 1 || s.tenant == "" {
				t.Fatal("credential selection or admission failed", response.Code, response.Body, s.calls)
			}
		})
	}
}
