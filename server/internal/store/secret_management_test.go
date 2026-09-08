package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
)

func TestSecretCreationPathsRecordManagementWorkspace(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	st := New(db)
	ids := mustSeedDevFixture(t, ctx, st)
	other, err := st.CreateWorkspace(ctx, CreateWorkspaceInput{Name: "Other owner", CreatedBy: ids.UserID, Now: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := st.RegisterWorkspaceRuntimeCredential(ctx, RegisterWorkspaceRuntimeCredentialInput{
		WorkspaceID: ids.WorkspaceID, Name: "Runtime test", Kind: "runtime", Provider: "test", AuthType: "api_key",
		EncryptedPayload: []byte(`{}`), CreatedBy: ids.UserID, Now: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	imported, err := st.ImportCapability(ctx, ImportCapabilityInput{
		WorkspaceID: ids.WorkspaceID, Name: "Inline test", Visibility: "workspace", Type: "mcp", CreatorID: ids.UserID, Version: "1.0.0",
		SourcePayload: []byte(`{}`),
		Spec: canonical.Spec{SchemaVersion: canonical.SchemaVersionCurrent, Kind: canonical.KindMCP,
			MCP: &canonical.MCPSpec{Servers: []canonical.MCPServer{{Name: "test", Command: "test", StartupTimeoutSec: 30,
				Env: map[string]canonical.EnvValue{"TOKEN": {Mode: canonical.EnvModeInlineSecret}},
			}}}},
		InlineSecrets: []ImportInlineSecret{{ServerName: "test", EnvKey: "TOKEN", SecretName: "Imported test", EncryptedPayload: []byte(`{}`)}},
	})
	if err != nil || len(imported.CreatedSecretIDs) != 1 {
		t.Fatalf("import: %v", err)
	}
	shared, err := st.CreateSecret(ctx, CreateSecretInput{
		ManagementWorkspaceID: ids.WorkspaceID, Name: "Shared inline test", Kind: "capability_inline", Provider: "test", AuthType: "literal",
	}, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetSecretPayload(ctx, other.Workspace.ID, shared.ID); err != nil {
		t.Fatalf("management ownership must not narrow existing shared reads: %v", err)
	}
	for _, id := range []string{runtime.ID, imported.CreatedSecretIDs[0], shared.ID} {
		secret, err := st.GetSecretPayload(ctx, ids.WorkspaceID, id)
		if err != nil || secret.ManagementWorkspaceID != ids.WorkspaceID {
			t.Fatalf("missing management workspace: %v", err)
		}
		if _, err := st.DisableSecret(ctx, other.Workspace.ID, id); !errors.Is(err, ErrUnknownSecret) {
			t.Fatalf("foreign disable: %v", err)
		}
		if _, err := st.DisableSecret(ctx, ids.WorkspaceID, id); err != nil {
			t.Fatalf("owner disable: %v", err)
		}
	}
}
