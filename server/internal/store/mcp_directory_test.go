package store

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
)

func TestMCPDirectoryImportPersistsProvenanceWithoutSecretsOrBindings(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	st := New(db)
	ids := mustSeedDevFixture(t, ctx, st)

	var secretsBefore int
	if err := db.QueryRow(ctx, `select count(*) from secrets`).Scan(&secretsBefore); err != nil {
		t.Fatal(err)
	}
	source := json.RawMessage(`{"source_format":"mcp_catalog","catalog_id":"filesystem","catalog_version":"1.0.0"}`)
	result, err := st.ImportCapability(ctx, ImportCapabilityInput{
		WorkspaceID:   ids.WorkspaceID,
		Name:          "Directory Filesystem",
		Description:   "Read and write configured files.",
		Visibility:    "workspace",
		Type:          "mcp",
		CreatorID:     ids.UserID,
		Version:       "1.0.0",
		SourcePayload: source,
		Spec: canonical.Spec{
			SchemaVersion: canonical.SchemaVersionCurrent,
			Kind:          canonical.KindMCP,
			MCP: &canonical.MCPSpec{Servers: []canonical.MCPServer{{
				Name:              "filesystem",
				Command:           "npx",
				Args:              []string{"-y", "@modelcontextprotocol/server-filesystem@1.0.0"},
				Env:               map[string]canonical.EnvValue{"FILESYSTEM_ROOT": {Mode: canonical.EnvModeLiteral}},
				StartupTimeoutSec: 30,
			}}},
		},
	})
	if err != nil {
		t.Fatalf("ImportCapability: %v", err)
	}
	if result.Capability.Type != "mcp" || result.Capability.Visibility != "workspace" {
		t.Fatalf("capability=%+v", result.Capability)
	}
	if len(result.CreatedSecretIDs) != 0 {
		t.Fatalf("created secrets=%v", result.CreatedSecretIDs)
	}

	installs, err := st.ListMCPDirectoryInstalls(ctx, ids.WorkspaceID)
	if err != nil {
		t.Fatalf("ListMCPDirectoryInstalls: %v", err)
	}
	if len(installs) != 1 || installs[0].CatalogID != "filesystem" || installs[0].CapabilityID != result.Capability.ID {
		t.Fatalf("installs=%+v", installs)
	}

	var bindings, secretsAfter int
	if err := db.QueryRow(ctx, `select count(*) from agent_capabilities where capability_id = $1`, result.Capability.ID).Scan(&bindings); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `select count(*) from secrets`).Scan(&secretsAfter); err != nil {
		t.Fatal(err)
	}
	if bindings != 0 {
		t.Fatalf("agent bindings=%d, want 0", bindings)
	}
	if secretsAfter != secretsBefore {
		t.Fatalf("secret count changed from %d to %d", secretsBefore, secretsAfter)
	}

	var stored json.RawMessage
	if err := db.QueryRow(ctx, `select source_payload from capability_version where id = $1`, result.CapabilityVersion.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	var provenance map[string]string
	if err := json.Unmarshal(stored, &provenance); err != nil {
		t.Fatal(err)
	}
	if provenance["catalog_id"] != "filesystem" || provenance["catalog_version"] != "1.0.0" {
		t.Fatalf("source_payload=%s", stored)
	}
}
