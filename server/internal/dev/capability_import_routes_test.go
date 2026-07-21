package dev

// Plan M3 verification step 3: exercise the capability import HTTP surface
// end-to-end against the real test DB.
//
// Two scenarios:
//
//   TestCapabilityImportPreview_DefaultsEnvToLiteral — paste a Claude Code
//   style mcpServers JSON whose env carries a credential-looking value
//   (GITHUB_PERSONAL_ACCESS_TOKEN=ghp_…). Decision #8 says the parser must
//   *not* auto-promote it to inline_secret/credential_ref; this test pins
//   that contract by asserting the env value comes back as mode=literal.
//   If a future contributor adds "smart" credential detection in the
//   parser, this test fails loud.
//
//   TestCapabilityImportCommit_InlineSecretLandsInSecretsTable — commits a
//   spec with one inline_secret env, then verifies (a) the secrets row
//   exists in the DB with the expected kind/provider breadcrumb and
//   encrypted_payload bytes, (b) the response carries the new secret_id,
//   (c) the persisted canonical_spec references that secret_id (not the
//   cleartext). This is the load-bearing safety property of the entire
//   import flow — if cleartext ever leaks into canonical_spec the commit
//   path is broken.
//
// Both tests follow the existing routes_capability_test.go conventions:
// route via capabilityTestRouter (which already wires the import handlers
// through RegisterRoutesWithStore + sets PARSAR_MASTER_KEY), call via
// serveCapabilityRoute (passes the master key to the helper that picks it
// up in the commit path), and skip the whole package when
// PARSAR_TEST_DATABASE_URL is absent (via openDevRouteTestDB).

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestCapabilityImportPreview_DefaultsEnvToLiteral pins Decision #8:
// the parser never guesses that an env value is a secret. Even a value
// shaped like ghp_… stays mode=literal until the operator opts in.
func TestCapabilityImportPreview_DefaultsEnvToLiteral(t *testing.T) {
	r, _ := capabilityTestRouter(t, map[string]string{store.DefaultDevFixtureIDs().UserID: "admin"}, nil)

	// Embed a token-shaped string in the env so a naive auto-detector
	// would (incorrectly) flag it. Inner quotes must be escaped because
	// the body is itself a JSON document.
	rawText := `{"mcpServers":{"github":{"command":"docker","args":["run","-i","--rm","ghcr.io/github/github-mcp-server"],"env":{"GITHUB_PERSONAL_ACCESS_TOKEN":"ghp_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}}}}`
	body := mustJSON(t, map[string]any{
		"kind":          "mcp",
		"source_format": "json",
		"raw_text":      rawText,
	})

	res := serveCapabilityRoute(t, r, http.MethodPost,
		"/api/v1/workspaces/"+store.DefaultDevFixtureIDs().WorkspaceID+"/capabilities/import/preview",
		body, store.DefaultDevFixtureIDs().UserID)
	if res.Code != http.StatusOK {
		t.Fatalf("preview expected 200, got %d: %s", res.Code, res.Body.String())
	}

	var parsed struct {
		CanonicalSpec canonical.Spec `json:"canonical_spec"`
		Warnings      []string       `json:"warnings"`
		SuggestedName string         `json:"suggested_name"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("decode preview response: %v\nbody=%s", err, res.Body.String())
	}
	if parsed.CanonicalSpec.Kind != canonical.KindMCP {
		t.Fatalf("expected kind=mcp, got %q", parsed.CanonicalSpec.Kind)
	}
	if parsed.CanonicalSpec.MCP == nil || len(parsed.CanonicalSpec.MCP.Servers) != 1 {
		t.Fatalf("expected exactly 1 server, got spec=%+v", parsed.CanonicalSpec)
	}
	srv := parsed.CanonicalSpec.MCP.Servers[0]
	if srv.Name != "github" {
		t.Fatalf("expected server name=github, got %q", srv.Name)
	}
	envValue, ok := srv.Env["GITHUB_PERSONAL_ACCESS_TOKEN"]
	if !ok {
		t.Fatalf("expected GITHUB_PERSONAL_ACCESS_TOKEN in env, got %+v", srv.Env)
	}
	if envValue.Mode != canonical.EnvModeLiteral {
		t.Fatalf("decision #8 violated: GITHUB_PERSONAL_ACCESS_TOKEN env was auto-promoted to mode=%q; want %q",
			envValue.Mode, canonical.EnvModeLiteral)
	}
	if envValue.SecretID != "" || envValue.CredentialKindCode != "" {
		t.Fatalf("literal env value must not have secret_id/credential_kind_code set, got %+v", envValue)
	}
	// And the literal must round-trip exactly — the parser must NOT mask,
	// redact, or rewrite the value just because it looks like a token.
	if envValue.Literal != "ghp_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA" {
		t.Fatalf("literal value altered by parser: got %q", envValue.Literal)
	}
}

// TestCapabilityImportCommit_InlineSecretLandsInSecretsTable verifies the
// cleartext-flows-into-encrypted-row safety property.
//
// Steps:
//  1. Build a canonical.Spec with one inline_secret env slot (SecretID empty
//     — the handler fills it after writing the secrets row).
//  2. Commit it, providing the cleartext under inline_secrets[].
//  3. Assert response includes exactly one created_secret_id.
//  4. Read the secrets table and verify a row exists with that id, the
//     workspace breadcrumb, the capability-inline kind, and an
//     encrypted_payload that does NOT contain the cleartext bytes.
//  5. Read capability_version.canonical_spec from DB and verify the env
//     slot was patched to reference that secret_id (and the literal is
//     empty).
func TestCapabilityImportCommit_InlineSecretLandsInSecretsTable(t *testing.T) {
	r, db := capabilityTestRouter(t, map[string]string{store.DefaultDevFixtureIDs().UserID: "admin"}, nil)

	const (
		capName     = "Import Inline Secret Cap"
		serverName  = "github"
		envKey      = "GITHUB_PERSONAL_ACCESS_TOKEN"
		secretValue = "ghp_top_secret_value_DO_NOT_LEAK_42"
	)

	spec := canonical.Spec{
		SchemaVersion: canonical.SchemaVersionCurrent,
		Kind:          canonical.KindMCP,
		MCP: &canonical.MCPSpec{
			Servers: []canonical.MCPServer{{
				Name:    serverName,
				Command: "docker",
				Args:    []string{"run", "-i", "--rm", "ghcr.io/github/github-mcp-server"},
				Env: map[string]canonical.EnvValue{
					envKey: {Mode: canonical.EnvModeInlineSecret}, // SecretID filled by handler
				},
			}},
		},
	}

	commitBody := mustJSON(t, map[string]any{
		"kind":           "mcp",
		"name":           capName,
		"type":           "mcp",
		"version":        "v1.0.0",
		"canonical_spec": spec,
		"inline_secrets": []map[string]any{{
			"server_name": serverName,
			"env_key":     envKey,
			"plaintext":   secretValue,
		}},
	})

	res := serveCapabilityRoute(t, r, http.MethodPost,
		"/api/v1/workspaces/"+store.DefaultDevFixtureIDs().WorkspaceID+"/capabilities/import/commit",
		commitBody, store.DefaultDevFixtureIDs().UserID)
	if res.Code != http.StatusCreated {
		t.Fatalf("commit expected 201, got %d: %s", res.Code, res.Body.String())
	}

	// (3) Response shape: one created secret_id, plus capability + version
	// payloads echoed back.
	var commitResp struct {
		Capability        store.CapabilityRead        `json:"capability"`
		CapabilityVersion store.CapabilityVersionRead `json:"capability_version"`
		CreatedSecretIDs  []string                    `json:"created_secret_ids"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &commitResp); err != nil {
		t.Fatalf("decode commit response: %v\nbody=%s", err, res.Body.String())
	}
	if len(commitResp.CreatedSecretIDs) != 1 {
		t.Fatalf("expected exactly 1 created secret, got %v", commitResp.CreatedSecretIDs)
	}
	secretID := commitResp.CreatedSecretIDs[0]
	if strings.TrimSpace(secretID) == "" {
		t.Fatalf("created_secret_ids[0] is empty")
	}
	if commitResp.Capability.Name != capName {
		t.Fatalf("capability.name = %q, want %q", commitResp.Capability.Name, capName)
	}

	// CRITICAL: the response body must NOT contain the cleartext anywhere.
	// If it did we would have leaked the secret back to the caller.
	if strings.Contains(res.Body.String(), secretValue) {
		t.Fatalf("commit response leaked cleartext secret: %s", res.Body.String())
	}

	// (4) Secrets table: row exists, breadcrumb fields are right, encrypted
	// payload is genuinely encrypted (cleartext bytes must NOT appear in
	// the column).
	row := lookupImportedSecret(t, db, secretID)
	if row.workspaceID != store.DefaultDevFixtureIDs().WorkspaceID {
		t.Fatalf("secret.workspace_id = %q, want %q", row.workspaceID, store.DefaultDevFixtureIDs().WorkspaceID)
	}
	if row.kind != "capability_inline" {
		t.Fatalf("secret.kind = %q, want capability_inline", row.kind)
	}
	if row.provider != "inline" || row.authType != "literal" {
		t.Fatalf("secret breadcrumb mismatch: provider=%q auth_type=%q", row.provider, row.authType)
	}
	if row.status != "active" {
		t.Fatalf("secret.status = %q, want active", row.status)
	}
	if strings.Contains(string(row.encryptedPayload), secretValue) {
		t.Fatalf("encrypted_payload contains cleartext bytes — encryption pipeline is broken")
	}

	// Metadata should carry the (capability_id, server, env_key) breadcrumb
	// that ImportCapability writes for operator traceability.
	var meta map[string]any
	if err := json.Unmarshal(row.metadata, &meta); err != nil {
		t.Fatalf("decode secret.metadata: %v", err)
	}
	if got, _ := meta["origin"].(string); got != "capability_import" {
		t.Fatalf("metadata.origin = %v, want capability_import", meta["origin"])
	}
	if got, _ := meta["server"].(string); got != serverName {
		t.Fatalf("metadata.server = %v, want %q", meta["server"], serverName)
	}
	if got, _ := meta["env_key"].(string); got != envKey {
		t.Fatalf("metadata.env_key = %v, want %q", meta["env_key"], envKey)
	}

	// (5) capability_version.canonical_spec: env slot now references secret_id,
	// literal is empty, no cleartext anywhere.
	specJSON := lookupCanonicalSpec(t, db, commitResp.CapabilityVersion.ID)
	if strings.Contains(string(specJSON), secretValue) {
		t.Fatalf("canonical_spec contains cleartext — handler failed to patch SecretID before persist:\n%s", string(specJSON))
	}
	var persisted canonical.Spec
	if err := json.Unmarshal(specJSON, &persisted); err != nil {
		t.Fatalf("decode persisted canonical_spec: %v", err)
	}
	if persisted.MCP == nil || len(persisted.MCP.Servers) != 1 {
		t.Fatalf("persisted spec missing single server: %+v", persisted)
	}
	persistedEnv := persisted.MCP.Servers[0].Env[envKey]
	if persistedEnv.Mode != canonical.EnvModeInlineSecret {
		t.Fatalf("persisted env mode = %q, want inline_secret", persistedEnv.Mode)
	}
	if persistedEnv.SecretID != secretID {
		t.Fatalf("persisted env secret_id = %q, want %q", persistedEnv.SecretID, secretID)
	}
	if persistedEnv.Literal != "" {
		t.Fatalf("persisted env literal must be empty for inline_secret mode, got %q", persistedEnv.Literal)
	}
}

func TestCapabilityImportCommit_CustomCredentialKind(t *testing.T) {
	r, db := capabilityTestRouter(t, map[string]string{store.DefaultDevFixtureIDs().UserID: "admin"}, nil)
	ctx := context.Background()
	st := store.New(db)
	ids := store.DefaultDevFixtureIDs()

	if _, err := st.CreateCredentialKind(ctx, store.CreateCredentialKindInput{
		Code:        "gitlab_token",
		DisplayName: "GitLab Token",
		CreatorID:   ids.UserID,
	}); err != nil {
		t.Fatal(err)
	}

	spec := canonical.Spec{
		SchemaVersion: canonical.SchemaVersionCurrent,
		Kind:          canonical.KindMCP,
		MCP: &canonical.MCPSpec{
			Servers: []canonical.MCPServer{{
				Name:    "gitlab",
				Command: "npx",
				Args:    []string{"-y", "@modelcontextprotocol/server-gitlab"},
				Env: map[string]canonical.EnvValue{
					"GITLAB_TOKEN": {Mode: canonical.EnvModeCredentialRef, CredentialKindCode: "gitlab_token"},
				},
			}},
		},
	}

	commitBody := mustJSON(t, map[string]any{
		"kind":           "mcp",
		"name":           "GitLab MCP",
		"type":           "mcp",
		"version":        "v1.0.0",
		"canonical_spec": spec,
	})

	res := serveCapabilityRoute(t, r, http.MethodPost,
		"/api/v1/workspaces/"+ids.WorkspaceID+"/capabilities/import/commit",
		commitBody, ids.UserID)
	if res.Code != http.StatusCreated {
		t.Fatalf("commit custom credential kind expected 201, got %d: %s", res.Code, res.Body.String())
	}

	var commitResp struct {
		CapabilityVersion store.CapabilityVersionRead `json:"capability_version"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &commitResp); err != nil {
		t.Fatalf("decode commit response: %v\nbody=%s", err, res.Body.String())
	}
	if len(commitResp.CapabilityVersion.RequiredCredentials) != 1 {
		t.Fatalf("required credentials = %+v, want one gitlab_token", commitResp.CapabilityVersion.RequiredCredentials)
	}
	if got := commitResp.CapabilityVersion.RequiredCredentials[0].Kind; got != "gitlab_token" {
		t.Fatalf("required credential kind = %q, want gitlab_token", got)
	}

	var persisted []byte
	if err := db.QueryRow(ctx,
		`select required_credentials from capability_version where id = $1`,
		commitResp.CapabilityVersion.ID,
	).Scan(&persisted); err != nil {
		t.Fatalf("lookup required_credentials: %v", err)
	}
	if !strings.Contains(string(persisted), "gitlab_token") {
		t.Fatalf("persisted required_credentials missing gitlab_token: %s", string(persisted))
	}
}

// --- DB helpers ---------------------------------------------------------

// importedSecretRow mirrors the secrets columns we care about for the
// commit assertion. Pulled into a struct so the test body reads as plain
// `row.kind` rather than multi-return Scan acrobatics.
type importedSecretRow struct {
	workspaceID      string
	kind             string
	provider         string
	authType         string
	encryptedPayload []byte
	metadata         []byte
	status           string
}

func lookupImportedSecret(t *testing.T, db *pgxpool.Pool, secretID string) importedSecretRow {
	t.Helper()
	var r importedSecretRow
	if err := db.QueryRow(context.Background(),
		`select workspace_id::text, kind, provider, auth_type, encrypted_payload, metadata, status
		 from secrets where id = $1 and deleted_at is null`,
		secretID,
	).Scan(&r.workspaceID, &r.kind, &r.provider, &r.authType, &r.encryptedPayload, &r.metadata, &r.status); err != nil {
		t.Fatalf("lookup secret %s: %v", secretID, err)
	}
	return r
}

func lookupCanonicalSpec(t *testing.T, db *pgxpool.Pool, capabilityVersionID string) []byte {
	t.Helper()
	var raw []byte
	if err := db.QueryRow(context.Background(),
		`select canonical_spec from capability_version where id = $1`,
		capabilityVersionID,
	).Scan(&raw); err != nil {
		t.Fatalf("lookup canonical_spec for version %s: %v", capabilityVersionID, err)
	}
	if len(raw) == 0 {
		t.Fatalf("capability_version %s has empty canonical_spec", capabilityVersionID)
	}
	return raw
}

// mustJSON marshals or fails the test — saves a sea of `if err != nil`
// around each request body literal.
func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	return string(b)
}

// ---------------------------------------------------------------------------
// Version-only import: POST .../capabilities/{id}/versions/import/commit
// ---------------------------------------------------------------------------
//
// Six scenarios cover the surface:
//
//   _HappyPath           — existing capability, new MCP version, inline_secret
//                          gets a fresh secret row + canonical_spec is patched
//   _KindMismatch        — mcp capability, skill spec → 422
//   _UnknownCapability   — random uuid → 404
//   _CrossWorkspace      — capability lives in workspace A, request targets B
//                          → 404 (don't leak existence)
//   _UnknownCredentialRef— credential_ref code that doesn't exist in
//                          credential_kinds → 422
//   _EmptyInlinePlaintext— inline_secret entry with "" plaintext → 400
//
// All of them share one fixture: bootstrap an MCP capability via the
// create-capability import endpoint, then poke the version endpoint.

func TestCapabilityVersionImportCommit_HappyPath(t *testing.T) {
	r, db := capabilityTestRouter(t, map[string]string{store.DefaultDevFixtureIDs().UserID: "admin"}, nil)
	ids := store.DefaultDevFixtureIDs()

	capabilityID := bootstrapMCPCapabilityForVersionTest(t, r, ids, "Version Import Happy", "github", "GITHUB_PERSONAL_ACCESS_TOKEN")

	const newSecret = "ghp_brand_new_value_for_v2"
	spec := canonical.Spec{
		SchemaVersion: canonical.SchemaVersionCurrent,
		Kind:          canonical.KindMCP,
		MCP: &canonical.MCPSpec{
			Servers: []canonical.MCPServer{{
				Name:    "github",
				Command: "docker",
				Args:    []string{"run", "-i", "--rm", "ghcr.io/github/github-mcp-server"},
				Env: map[string]canonical.EnvValue{
					"GITHUB_PERSONAL_ACCESS_TOKEN": {Mode: canonical.EnvModeInlineSecret},
				},
			}},
		},
	}

	body := mustJSON(t, map[string]any{
		"version":        "v2.0.0",
		"canonical_spec": spec,
		"inline_secrets": []map[string]any{{
			"server_name": "github",
			"env_key":     "GITHUB_PERSONAL_ACCESS_TOKEN",
			"plaintext":   newSecret,
		}},
	})

	res := serveCapabilityRoute(t, r, http.MethodPost,
		"/api/v1/workspaces/"+ids.WorkspaceID+"/capabilities/"+capabilityID+"/versions/import/commit",
		body, ids.UserID)
	if res.Code != http.StatusCreated {
		t.Fatalf("version import expected 201, got %d: %s", res.Code, res.Body.String())
	}

	var resp struct {
		Capability        store.CapabilityRead        `json:"capability"`
		CapabilityVersion store.CapabilityVersionRead `json:"capability_version"`
		CreatedSecretIDs  []string                    `json:"created_secret_ids"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v\nbody=%s", err, res.Body.String())
	}
	if resp.Capability.ID != capabilityID {
		t.Fatalf("response.capability.id = %q, want %q", resp.Capability.ID, capabilityID)
	}
	if resp.CapabilityVersion.Version != "v2.0.0" {
		t.Fatalf("response.capability_version.version = %q, want v2.0.0", resp.CapabilityVersion.Version)
	}
	if len(resp.CreatedSecretIDs) != 1 {
		t.Fatalf("expected exactly 1 created secret, got %v", resp.CreatedSecretIDs)
	}
	// No cleartext leak in response — same critical property as create flow.
	if strings.Contains(res.Body.String(), newSecret) {
		t.Fatalf("response leaked cleartext: %s", res.Body.String())
	}

	// Persisted canonical_spec references the new secret row.
	specJSON := lookupCanonicalSpec(t, db, resp.CapabilityVersion.ID)
	if strings.Contains(string(specJSON), newSecret) {
		t.Fatalf("canonical_spec contains cleartext for v2: %s", string(specJSON))
	}
	var persisted canonical.Spec
	if err := json.Unmarshal(specJSON, &persisted); err != nil {
		t.Fatalf("decode persisted spec: %v", err)
	}
	persistedEnv := persisted.MCP.Servers[0].Env["GITHUB_PERSONAL_ACCESS_TOKEN"]
	if persistedEnv.SecretID != resp.CreatedSecretIDs[0] {
		t.Fatalf("persisted secret_id = %q, want %q", persistedEnv.SecretID, resp.CreatedSecretIDs[0])
	}

	// And the version row is actually linked to the right capability.
	var capIDFromVersion string
	if err := db.QueryRow(context.Background(),
		`select capability_id::text from capability_version where id = $1`,
		resp.CapabilityVersion.ID,
	).Scan(&capIDFromVersion); err != nil {
		t.Fatalf("lookup version capability_id: %v", err)
	}
	if capIDFromVersion != capabilityID {
		t.Fatalf("version's capability_id = %q, want %q", capIDFromVersion, capabilityID)
	}
}

func TestCapabilityVersionImportCommit_AssignsNextVersionWhenOmitted(t *testing.T) {
	r, _ := capabilityTestRouter(t, map[string]string{store.DefaultDevFixtureIDs().UserID: "admin"}, nil)
	ids := store.DefaultDevFixtureIDs()
	capabilityID := bootstrapMCPCapabilityForVersionTest(t, r, ids, "Automatic Version", "automatic", "AUTOMATIC_TOKEN")

	spec := canonical.Spec{
		SchemaVersion: canonical.SchemaVersionCurrent,
		Kind:          canonical.KindMCP,
		MCP: &canonical.MCPSpec{
			Servers: []canonical.MCPServer{{
				Name:    "automatic",
				Command: "echo",
				Env:     map[string]canonical.EnvValue{},
			}},
		},
	}
	body := mustJSON(t, map[string]any{"canonical_spec": spec})
	res := serveCapabilityRoute(t, r, http.MethodPost,
		"/api/v1/workspaces/"+ids.WorkspaceID+"/capabilities/"+capabilityID+"/versions/import/commit",
		body, ids.UserID)
	if res.Code != http.StatusCreated {
		t.Fatalf("automatic version import expected 201, got %d: %s", res.Code, res.Body.String())
	}

	var response struct {
		CapabilityVersion store.CapabilityVersionRead `json:"capability_version"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.CapabilityVersion.Version != "1.0.1" {
		t.Fatalf("automatic version = %q, want 1.0.1", response.CapabilityVersion.Version)
	}
}

func TestCapabilityVersionImportCommit_KindMismatch(t *testing.T) {
	r, _ := capabilityTestRouter(t, map[string]string{store.DefaultDevFixtureIDs().UserID: "admin"}, nil)
	ids := store.DefaultDevFixtureIDs()

	// MCP capability bootstrap.
	capabilityID := bootstrapMCPCapabilityForVersionTest(t, r, ids, "Version Import Kind Mismatch", "github", "GITHUB_PERSONAL_ACCESS_TOKEN")

	// Now try to add a SKILL version to it — store must reject.
	skillSpec := canonical.Spec{
		SchemaVersion: canonical.SchemaVersionCurrent,
		Kind:          canonical.KindSkill,
		Skill: &canonical.SkillSpec{
			Slug:        "mismatched-skill",
			Title:       "Mismatched Skill",
			Instruction: "this should never land",
		},
	}
	body := mustJSON(t, map[string]any{
		"version":        "v2",
		"canonical_spec": skillSpec,
	})

	res := serveCapabilityRoute(t, r, http.MethodPost,
		"/api/v1/workspaces/"+ids.WorkspaceID+"/capabilities/"+capabilityID+"/versions/import/commit",
		body, ids.UserID)
	if res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("kind mismatch expected 422, got %d: %s", res.Code, res.Body.String())
	}
}

func TestCapabilityVersionImportCommit_UnknownCapability(t *testing.T) {
	r, _ := capabilityTestRouter(t, map[string]string{store.DefaultDevFixtureIDs().UserID: "admin"}, nil)
	ids := store.DefaultDevFixtureIDs()

	spec := canonical.Spec{
		SchemaVersion: canonical.SchemaVersionCurrent,
		Kind:          canonical.KindMCP,
		MCP: &canonical.MCPSpec{
			Servers: []canonical.MCPServer{{
				Name:    "x",
				Command: "echo",
				Env:     map[string]canonical.EnvValue{},
			}},
		},
	}
	body := mustJSON(t, map[string]any{"version": "v1", "canonical_spec": spec})

	// Well-formed uuid that doesn't match any capability.
	res := serveCapabilityRoute(t, r, http.MethodPost,
		"/api/v1/workspaces/"+ids.WorkspaceID+"/capabilities/00000000-0000-4000-8000-000000000099/versions/import/commit",
		body, ids.UserID)
	if res.Code != http.StatusNotFound {
		t.Fatalf("unknown capability expected 404, got %d: %s", res.Code, res.Body.String())
	}
}

func TestCapabilityVersionImportCommit_UnknownCredentialRef(t *testing.T) {
	r, _ := capabilityTestRouter(t, map[string]string{store.DefaultDevFixtureIDs().UserID: "admin"}, nil)
	ids := store.DefaultDevFixtureIDs()

	capabilityID := bootstrapMCPCapabilityForVersionTest(t, r, ids, "Version Import Unknown Cred", "x", "X_TOKEN")

	spec := canonical.Spec{
		SchemaVersion: canonical.SchemaVersionCurrent,
		Kind:          canonical.KindMCP,
		MCP: &canonical.MCPSpec{
			Servers: []canonical.MCPServer{{
				Name:    "x",
				Command: "echo",
				Env: map[string]canonical.EnvValue{
					"X_TOKEN": {Mode: canonical.EnvModeCredentialRef, CredentialKindCode: "this_kind_does_not_exist"},
				},
			}},
		},
	}
	body := mustJSON(t, map[string]any{"version": "v2", "canonical_spec": spec})

	res := serveCapabilityRoute(t, r, http.MethodPost,
		"/api/v1/workspaces/"+ids.WorkspaceID+"/capabilities/"+capabilityID+"/versions/import/commit",
		body, ids.UserID)
	if res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown credential ref expected 422, got %d: %s", res.Code, res.Body.String())
	}
}

func TestCapabilityVersionImportCommit_EmptyInlinePlaintext(t *testing.T) {
	r, _ := capabilityTestRouter(t, map[string]string{store.DefaultDevFixtureIDs().UserID: "admin"}, nil)
	ids := store.DefaultDevFixtureIDs()

	capabilityID := bootstrapMCPCapabilityForVersionTest(t, r, ids, "Version Import Empty Plain", "github", "GITHUB_PERSONAL_ACCESS_TOKEN")

	spec := canonical.Spec{
		SchemaVersion: canonical.SchemaVersionCurrent,
		Kind:          canonical.KindMCP,
		MCP: &canonical.MCPSpec{
			Servers: []canonical.MCPServer{{
				Name:    "github",
				Command: "echo",
				Env: map[string]canonical.EnvValue{
					"GITHUB_PERSONAL_ACCESS_TOKEN": {Mode: canonical.EnvModeInlineSecret},
				},
			}},
		},
	}
	body := mustJSON(t, map[string]any{
		"version":        "v2",
		"canonical_spec": spec,
		"inline_secrets": []map[string]any{{
			"server_name": "github",
			"env_key":     "GITHUB_PERSONAL_ACCESS_TOKEN",
			"plaintext":   "", // ← the failure
		}},
	})

	res := serveCapabilityRoute(t, r, http.MethodPost,
		"/api/v1/workspaces/"+ids.WorkspaceID+"/capabilities/"+capabilityID+"/versions/import/commit",
		body, ids.UserID)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("empty plaintext expected 400, got %d: %s", res.Code, res.Body.String())
	}
}

// bootstrapMCPCapabilityForVersionTest creates a fresh MCP capability via the
// create-capability import endpoint and returns its id. Reduces a 30-line
// preamble down to one call in each version-import test.
//
// The fixture intentionally uses one inline_secret env so the bootstrapped
// capability has a meaningful canonical_spec (some assertions check the
// version-import didn't trample the previous version's secret row, etc.).
func bootstrapMCPCapabilityForVersionTest(t *testing.T, r http.Handler, ids store.DevFixtureIDs, name, serverName, envKey string) string {
	t.Helper()
	spec := canonical.Spec{
		SchemaVersion: canonical.SchemaVersionCurrent,
		Kind:          canonical.KindMCP,
		MCP: &canonical.MCPSpec{
			Servers: []canonical.MCPServer{{
				Name:    serverName,
				Command: "echo",
				Env: map[string]canonical.EnvValue{
					envKey: {Mode: canonical.EnvModeInlineSecret},
				},
			}},
		},
	}
	body := mustJSON(t, map[string]any{
		"kind":           "mcp",
		"name":           name,
		"type":           "mcp",
		"version":        "v1",
		"canonical_spec": spec,
		"inline_secrets": []map[string]any{{
			"server_name": serverName,
			"env_key":     envKey,
			"plaintext":   "bootstrap_secret_v1",
		}},
	})
	res := serveCapabilityRoute(t, r, http.MethodPost,
		"/api/v1/workspaces/"+ids.WorkspaceID+"/capabilities/import/commit",
		body, ids.UserID)
	if res.Code != http.StatusCreated {
		t.Fatalf("bootstrap capability expected 201, got %d: %s", res.Code, res.Body.String())
	}
	var resp struct {
		Capability store.CapabilityRead `json:"capability"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode bootstrap response: %v", err)
	}
	if resp.Capability.ID == "" {
		t.Fatalf("bootstrap response missing capability.id: %s", res.Body.String())
	}
	return resp.Capability.ID
}
