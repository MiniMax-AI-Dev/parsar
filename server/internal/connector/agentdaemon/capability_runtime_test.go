package agentdaemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth/mcpoauth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/secrets"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// ---------------------------------------------------------------------------
// stub: capability store
// ---------------------------------------------------------------------------

type stubCapabilityStore struct {
	rows        []store.EnabledCapabilityRead
	err         error
	credentials map[string]store.UserCredentialRead // key = "userID:kind"

	// builtinDisabled maps capability_key -> true when the built-in should
	// report as OFF. Absence => default ON (mirrors the store's no-row
	// semantics). builtinErr, when set, is returned for every lookup.
	builtinDisabled map[string]bool
	builtinErr      error
}

func (s stubCapabilityStore) GetEnabledCapabilitiesForAgent(_ context.Context, _ string) ([]store.EnabledCapabilityRead, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.rows, nil
}

func (s stubCapabilityStore) GetUserCredentialByUserKind(_ context.Context, userID, kind string) (store.UserCredentialRead, bool, error) {
	key := userID + ":" + kind
	cred, ok := s.credentials[key]
	return cred, ok, nil
}

func (s stubCapabilityStore) IsBuiltinCapabilityEnabled(_ context.Context, _, key string) (bool, error) {
	if s.builtinErr != nil {
		return false, s.builtinErr
	}
	if s.builtinDisabled[key] {
		return false, nil
	}
	return true, nil
}

// ---------------------------------------------------------------------------
// test helpers
// ---------------------------------------------------------------------------

const testMasterKey = "test-master-key-for-unit-tests!!" // 32 bytes for AES-256

func testSecretsService(t *testing.T) *secrets.Service {
	t.Helper()
	svc, err := secrets.New(testMasterKey)
	if err != nil {
		t.Fatalf("secrets.New: %v", err)
	}
	return svc
}

func encryptPayload(t *testing.T, svc *secrets.Service, payload map[string]any) []byte {
	t.Helper()
	ct, err := svc.Encrypt(payload)
	if err != nil {
		t.Fatalf("secrets.Encrypt: %v", err)
	}
	return ct
}

// ---------------------------------------------------------------------------
// test fixture builders
// ---------------------------------------------------------------------------

// newSkillRow builds a skill row already migrated to the OSS-zip path
// (oss_key + sha256 columns populated). Legacy markdown-paste rows
// without oss_key are built via newLegacySkillRow.
func newSkillRow(t *testing.T, id, name, instruction string) store.EnabledCapabilityRead {
	t.Helper()
	spec := canonical.Spec{
		SchemaVersion: canonical.SchemaVersionCurrent,
		Kind:          canonical.KindSkill,
		Skill: &canonical.SkillSpec{
			Slug:        "skill-" + id,
			Title:       name,
			Instruction: instruction,
		},
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal canonical: %v", err)
	}
	return store.EnabledCapabilityRead{
		CapabilityID:  id,
		Name:          name,
		Type:          "skill",
		CanonicalSpec: raw,
		Version:       "1.0.0",
		OssKey:        "capabilities/skills/ws/" + id + "/skill.zip",
		SHA256:        validSHA256,
	}
}

// newLegacySkillRow builds a markdown-paste-era skill: canonical_spec
// present, oss_key/sha256 empty. The connector should skip these with
// a warning telling the operator to re-upload as a zip.
func newLegacySkillRow(t *testing.T, id, name, instruction string) store.EnabledCapabilityRead {
	t.Helper()
	row := newSkillRow(t, id, name, instruction)
	row.OssKey = ""
	row.SHA256 = ""
	return row
}

func newMCPRow(t *testing.T, id, name string, servers []canonical.MCPServer, requiredCreds []store.RequiredCredential) store.EnabledCapabilityRead {
	t.Helper()
	spec := canonical.Spec{
		SchemaVersion: canonical.SchemaVersionCurrent,
		Kind:          canonical.KindMCP,
		MCP:           &canonical.MCPSpec{Servers: servers},
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal canonical: %v", err)
	}
	return store.EnabledCapabilityRead{
		CapabilityID:        id,
		Name:                name,
		Type:                "mcp",
		CanonicalSpec:       raw,
		RequiredCredentials: requiredCreds,
	}
}

func defaultPromptInput() connector.PromptInput {
	return connector.PromptInput{
		AgentID:                 "pa-1",
		ConversationInitiatorID: "user-1",
		WorkspaceID:             "ws-1",
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// ---------------------------------------------------------------------------
// Skill tests
// ---------------------------------------------------------------------------

func TestResolveCapabilityAdditions_SkillsResolvedInOrder(t *testing.T) {
	presigner := &stubPluginPresigner{}
	c := &Connector{
		capabilities: stubCapabilityStore{
			rows: []store.EnabledCapabilityRead{
				newSkillRow(t, "a", "First", "Do A first."),
				newSkillRow(t, "b", "Second", "Then do B."),
			},
		},
		oss: presigner,
		log: discardLogger(),
	}
	got, err := c.resolveCapabilityAdditions(context.Background(), defaultPromptInput(), "claude_code")
	if err != nil {
		t.Fatalf("resolveCapabilityAdditions: %v", err)
	}
	if len(got.Skills) != 2 {
		t.Fatalf("want 2 skills, got %d: %+v", len(got.Skills), got.Skills)
	}
	if got.Skills[0].Name != "skill-a" || got.Skills[1].Name != "skill-b" {
		t.Fatalf("skill names out of order: %+v", got.Skills)
	}
	for i, s := range got.Skills {
		if s.SHA256 != validSHA256 {
			t.Fatalf("skills[%d].sha256 = %q", i, s.SHA256)
		}
		if s.DownloadURL == "" {
			t.Fatalf("skills[%d] missing download_url", i)
		}
		if s.Version != "1.0.0" {
			t.Fatalf("skills[%d].version = %q", i, s.Version)
		}
	}
}

func TestResolveCapabilityAdditions_NilStoreNoOp(t *testing.T) {
	c := &Connector{log: discardLogger()}
	got, err := c.resolveCapabilityAdditions(context.Background(), defaultPromptInput(), "claude_code")
	if err != nil {
		t.Fatalf("nil store: %v", err)
	}
	if got.Skills != nil || got.MCPServers != nil {
		t.Fatalf("expected nil slices when capabilities store missing, got %+v", got)
	}
}

func TestResolveCapabilityAdditions_SkillKindMismatchErrors(t *testing.T) {
	mismatched := canonical.Spec{
		SchemaVersion: canonical.SchemaVersionCurrent,
		Kind:          canonical.KindMCP,
		MCP: &canonical.MCPSpec{
			Servers: []canonical.MCPServer{
				{Name: "x", Command: "true"},
			},
		},
	}
	raw, err := json.Marshal(mismatched)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	row := store.EnabledCapabilityRead{
		CapabilityID:  "bad",
		Name:          "bad-row",
		Type:          "skill",
		CanonicalSpec: raw,
		// oss_key + sha256 populated so resolveSkillCapability reaches
		// the kind-mismatch check (empty oss_key would short-circuit).
		OssKey: "capabilities/skills/ws/bad/skill.zip",
		SHA256: validSHA256,
	}
	c := &Connector{
		capabilities: stubCapabilityStore{rows: []store.EnabledCapabilityRead{row}},
		oss:          &stubPluginPresigner{},
		log:          discardLogger(),
	}
	_, err = c.resolveCapabilityAdditions(context.Background(), defaultPromptInput(), "claude_code")
	if err == nil {
		t.Fatal("expected error for skill row with mcp canonical_spec")
	}
}

func TestResolveCapabilityAdditions_LegacyMarkdownSkillSurfacedAsDisabled(t *testing.T) {
	// Markdown-paste-era skill (canonical_spec present but oss_key /
	// sha256 empty), pinning_mode 'pinned' (the migration default).
	// The b77a1c1c-era silent skip is gone: the resolver now emits a
	// DisabledCapability with SubKind=CapabilityVersionUnavailable so
	// the user sees a system-message nudge instead of an invisible
	// failure. They can fix it by switching pinning_mode to 'latest'
	// (if a newer version exists) or re-uploading the skill.
	row := newLegacySkillRow(t, "old", "legacy", "stale instruction")
	row.PinningMode = store.PinningModePinned
	c := &Connector{
		capabilities: stubCapabilityStore{rows: []store.EnabledCapabilityRead{row}},
		oss:          &stubPluginPresigner{},
		log:          discardLogger(),
	}
	got, err := c.resolveCapabilityAdditions(context.Background(), defaultPromptInput(), "claude_code")
	if err != nil {
		t.Fatalf("legacy row: %v", err)
	}
	if len(got.Skills) != 0 {
		t.Fatalf("expected legacy skill not to resolve, got %+v", got.Skills)
	}
	if len(got.Disabled) != 1 {
		t.Fatalf("expected 1 DisabledCapability, got %d: %+v", len(got.Disabled), got.Disabled)
	}
	if got.Disabled[0].SubKind != CapabilityVersionUnavailable {
		t.Fatalf("Disabled[0].SubKind = %q, want %q", got.Disabled[0].SubKind, CapabilityVersionUnavailable)
	}
	if got.Disabled[0].CapabilityID != "old" {
		t.Fatalf("Disabled[0].CapabilityID = %q, want %q", got.Disabled[0].CapabilityID, "old")
	}
}

// TestResolveCapabilityAdditions_LatestModeFollowsLatestVersion covers
// the core promise of pinning_mode='latest': the resolver ignores the
// pinned cv.* columns and picks up the LatestOssKey / LatestSHA256 /
// LatestCanonicalSpec fields the SQL lateral subquery joined in. The
// scenario simulates the user's original bug: an old binding still
// points at a pre-b77a1c1c version (oss_key empty) but the capability
// has since been re-uploaded; under 'latest' mode the resolver picks
// up the new version automatically.
func TestResolveCapabilityAdditions_LatestModeFollowsLatestVersion(t *testing.T) {
	// "pinned" row would silent-skip (oss_key empty). Flip to 'latest'
	// and seed LatestOssKey/LatestSHA256/LatestCanonicalSpec to mimic
	// a fresh re-upload.
	freshSpec := canonical.Spec{
		SchemaVersion: canonical.SchemaVersionCurrent,
		Kind:          canonical.KindSkill,
		Skill: &canonical.SkillSpec{
			Slug:        "skill-old",
			Title:       "Fresh",
			Instruction: "fresh instruction",
		},
	}
	freshRaw, err := json.Marshal(freshSpec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	row := newLegacySkillRow(t, "old", "legacy", "old instruction")
	row.PinningMode = store.PinningModeLatest
	row.LatestOssKey = "capabilities/skills/ws/old/fresh.zip"
	row.LatestSHA256 = validSHA256
	row.LatestCanonicalSpec = freshRaw
	row.LatestVersion = "2.0.0"

	c := &Connector{
		capabilities: stubCapabilityStore{rows: []store.EnabledCapabilityRead{row}},
		oss:          &stubPluginPresigner{},
		log:          discardLogger(),
	}
	got, err := c.resolveCapabilityAdditions(context.Background(), defaultPromptInput(), "claude_code")
	if err != nil {
		t.Fatalf("resolveCapabilityAdditions: %v", err)
	}
	if len(got.Skills) != 1 {
		t.Fatalf("expected 1 resolved skill, got %d: %+v", len(got.Skills), got.Skills)
	}
	if got.Skills[0].Version != "2.0.0" {
		t.Fatalf("latest mode picked the wrong Version: got %q, want %q (pinned cv.Version='1.0.0')",
			got.Skills[0].Version, "2.0.0")
	}
	if got.Skills[0].SHA256 != validSHA256 {
		t.Fatalf("latest mode did not propagate LatestSHA256: got %q", got.Skills[0].SHA256)
	}
	if len(got.Disabled) != 0 {
		t.Fatalf("latest mode should not emit DisabledCapability when latest_* is healthy, got %+v", got.Disabled)
	}
}

// TestResolveCapabilityAdditions_LatestModeStillUnavailableEmitsDisabled
// covers the worst-case: pinning_mode='latest' but the capability has
// no usable zip in any version (LatestOssKey empty). The resolver must
// still surface a DisabledCapability rather than silent-skip, otherwise
// the user has no signal at all.
func TestResolveCapabilityAdditions_LatestModeStillUnavailableEmitsDisabled(t *testing.T) {
	row := newLegacySkillRow(t, "neverUploaded", "ghost", "x")
	row.PinningMode = store.PinningModeLatest
	// LatestOssKey intentionally empty.

	c := &Connector{
		capabilities: stubCapabilityStore{rows: []store.EnabledCapabilityRead{row}},
		oss:          &stubPluginPresigner{},
		log:          discardLogger(),
	}
	got, err := c.resolveCapabilityAdditions(context.Background(), defaultPromptInput(), "claude_code")
	if err != nil {
		t.Fatalf("resolveCapabilityAdditions: %v", err)
	}
	if len(got.Skills) != 0 {
		t.Fatalf("expected no skill resolved, got %+v", got.Skills)
	}
	if len(got.Disabled) != 1 || got.Disabled[0].SubKind != CapabilityVersionUnavailable {
		t.Fatalf("expected one CapabilityVersionUnavailable DisabledCapability, got %+v", got.Disabled)
	}
}

func TestResolveCapabilityAdditions_SkillSkippedWhenPresignerNil(t *testing.T) {
	c := &Connector{
		capabilities: stubCapabilityStore{rows: []store.EnabledCapabilityRead{
			newSkillRow(t, "a", "Skill", "..."),
		}},
		log: discardLogger(),
	}
	got, err := c.resolveCapabilityAdditions(context.Background(), defaultPromptInput(), "claude_code")
	if err != nil {
		t.Fatalf("resolveCapabilityAdditions: %v", err)
	}
	if len(got.Skills) != 0 {
		t.Fatalf("skill count = %d, want 0 (nil presigner)", len(got.Skills))
	}
}

func TestResolveCapabilityAdditions_StoreErrorPropagates(t *testing.T) {
	sentinel := errors.New("db blip")
	c := &Connector{
		capabilities: stubCapabilityStore{err: sentinel},
		log:          discardLogger(),
	}
	_, err := c.resolveCapabilityAdditions(context.Background(), defaultPromptInput(), "claude_code")
	if err == nil || !errors.Is(err, sentinel) {
		t.Fatalf("expected store error to bubble, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// MCP tests
// ---------------------------------------------------------------------------

func TestResolveCapabilityAdditions_MCPNoCreds(t *testing.T) {
	row := newMCPRow(t, "mcp-1", "github mcp", []canonical.MCPServer{
		{
			Name:    "github",
			Command: "npx",
			Args:    []string{"-y", "@modelcontextprotocol/server-github"},
			Env: map[string]canonical.EnvValue{
				"NODE_OPTIONS": {Mode: canonical.EnvModeLiteral, Literal: "--max-old-space-size=512"},
			},
		},
	}, nil)

	c := &Connector{
		capabilities: stubCapabilityStore{rows: []store.EnabledCapabilityRead{row}},
		log:          discardLogger(),
	}
	got, err := c.resolveCapabilityAdditions(context.Background(), defaultPromptInput(), "claude_code")
	if err != nil {
		t.Fatalf("resolveCapabilityAdditions: %v", err)
	}
	if len(got.MCPServers) != 1 {
		t.Fatalf("want 1 mcp server, got %d", len(got.MCPServers))
	}
	srv, ok := got.MCPServers["github"]
	if !ok {
		t.Fatal("missing server 'github'")
	}
	srvMap := srv.(map[string]any)
	if srvMap["command"] != "npx" {
		t.Fatalf("command = %v, want npx", srvMap["command"])
	}
	args, _ := srvMap["args"].([]string)
	if len(args) != 2 || args[0] != "-y" {
		t.Fatalf("args = %v, want [-y @modelcontextprotocol/server-github]", args)
	}
	env, _ := srvMap["env"].(map[string]string)
	if env["NODE_OPTIONS"] != "--max-old-space-size=512" {
		t.Fatalf("env NODE_OPTIONS = %v", env["NODE_OPTIONS"])
	}
}

func TestResolveCapabilityAdditions_MCPStreamableHTTP(t *testing.T) {
	row := newMCPRow(t, "mcp-http", "docs", []canonical.MCPServer{{
		Name:      "docs",
		Transport: canonical.MCPTransportStreamableHTTP,
		URL:       "https://docs.example.com/mcp",
	}}, nil)
	c := &Connector{
		capabilities: stubCapabilityStore{rows: []store.EnabledCapabilityRead{row}},
		log:          discardLogger(),
	}
	got, err := c.resolveCapabilityAdditions(context.Background(), defaultPromptInput(), "claude_code")
	if err != nil {
		t.Fatalf("resolveCapabilityAdditions: %v", err)
	}
	server := got.MCPServers["docs"].(map[string]any)
	if server["type"] != "http" || server["url"] != "https://docs.example.com/mcp" {
		t.Fatalf("server = %+v", server)
	}
}

func TestResolveCapabilityAdditions_UsesWorkspaceOAuthHeader(t *testing.T) {
	svc := testSecretsService(t)
	credential := mcpoauth.Credential{
		AccessToken:             "workspace-notion-token",
		RefreshToken:            "workspace-notion-refresh",
		ExpiresAt:               time.Now().Add(time.Hour),
		ClientID:                "client-1",
		TokenEndpointAuthMethod: "none",
		TokenEndpoint:           "https://mcp.notion.com/token",
		Resource:                "https://mcp.notion.com/mcp",
	}
	row := newMCPRow(t, "mcp-notion", "Notion", []canonical.MCPServer{{
		Name:      "notion",
		Transport: canonical.MCPTransportStreamableHTTP,
		URL:       "https://mcp.notion.com/mcp",
		Headers: map[string]canonical.EnvValue{
			"Authorization": {
				Mode:               canonical.EnvModeCredentialRef,
				Prefix:             "Bearer ",
				CredentialKindCode: capability.CredentialKindMCPOAuth,
			},
		},
	}}, []store.RequiredCredential{{Kind: capability.CredentialKindMCPOAuth, Required: true}})
	row.Configuration = map[string]any{
		"credential_bindings": map[string]any{
			capability.CredentialKindMCPOAuth: map[string]any{
				"source":    "shared",
				"secret_id": "secret-1",
			},
		},
	}
	c := &Connector{
		capabilities: stubCapabilityStore{rows: []store.EnabledCapabilityRead{row}},
		modelResolver: &fakeModelResolver{secret: store.SecretPayload{
			SecretRead:       store.SecretRead{ID: "secret-1", Status: "active"},
			EncryptedPayload: encryptPayload(t, svc, credential.Payload()),
		}},
		secrets: svc,
		log:     discardLogger(),
	}
	in := defaultPromptInput()
	in.ConversationInitiatorID = ""
	got, err := c.resolveCapabilityAdditions(context.Background(), in, "claude_code")
	if err != nil {
		t.Fatalf("resolveCapabilityAdditions: %v", err)
	}
	headers := got.MCPServers["notion"].(map[string]any)["headers"].(map[string]string)
	if headers["Authorization"] != "Bearer workspace-notion-token" {
		t.Fatalf("Authorization = %q", headers["Authorization"])
	}
}

func TestResolveCapabilityAdditions_UsesCapabilityScopedOAuthBindings(t *testing.T) {
	svc := testSecretsService(t)
	newCredential := func(token, resource string) mcpoauth.Credential {
		return mcpoauth.Credential{
			AccessToken:             token,
			ClientID:                "client-1",
			TokenEndpointAuthMethod: "none",
			TokenEndpoint:           resource + "/token",
			Resource:                resource,
		}
	}
	newRow := func(id, name, serverName, url, secretID string) store.EnabledCapabilityRead {
		row := newMCPRow(t, id, name, []canonical.MCPServer{{
			Name:      serverName,
			Transport: canonical.MCPTransportStreamableHTTP,
			URL:       url,
			Headers: map[string]canonical.EnvValue{
				"Authorization": {
					Mode:               canonical.EnvModeCredentialRef,
					Prefix:             "Bearer ",
					CredentialKindCode: capability.CredentialKindMCPOAuth,
				},
			},
		}}, []store.RequiredCredential{{Kind: capability.CredentialKindMCPOAuth, Required: true}})
		row.Configuration = map[string]any{
			"credential_bindings": map[string]any{
				capability.CredentialKindMCPOAuth: map[string]any{
					"source":    "shared",
					"secret_id": secretID,
				},
			},
		}
		return row
	}
	notionURL := "https://mcp.notion.com/mcp"
	githubURL := "https://api.githubcopilot.com/mcp"
	resolver := &fakeModelResolver{secrets: map[string]store.SecretPayload{
		"secret-notion": {
			SecretRead:       store.SecretRead{ID: "secret-notion", Status: "active"},
			EncryptedPayload: encryptPayload(t, svc, newCredential("notion-token", notionURL).Payload()),
		},
		"secret-github": {
			SecretRead:       store.SecretRead{ID: "secret-github", Status: "active"},
			EncryptedPayload: encryptPayload(t, svc, newCredential("github-token", githubURL).Payload()),
		},
	}}
	c := &Connector{
		capabilities: stubCapabilityStore{rows: []store.EnabledCapabilityRead{
			newRow("mcp-notion", "Notion", "notion", notionURL, "secret-notion"),
			newRow("mcp-github", "GitHub", "github", githubURL, "secret-github"),
		}},
		modelResolver: resolver,
		secrets:       svc,
		log:           discardLogger(),
	}
	in := defaultPromptInput()
	in.ConversationInitiatorID = ""
	got, err := c.resolveCapabilityAdditions(context.Background(), in, "claude_code")
	if err != nil {
		t.Fatalf("resolveCapabilityAdditions: %v", err)
	}
	notionHeaders := got.MCPServers["notion"].(map[string]any)["headers"].(map[string]string)
	githubHeaders := got.MCPServers["github"].(map[string]any)["headers"].(map[string]string)
	if notionHeaders["Authorization"] != "Bearer notion-token" {
		t.Fatalf("notion Authorization = %q", notionHeaders["Authorization"])
	}
	if githubHeaders["Authorization"] != "Bearer github-token" {
		t.Fatalf("github Authorization = %q", githubHeaders["Authorization"])
	}
}

func TestResolveCapabilityAdditions_MCPWithCredential(t *testing.T) {
	svc := testSecretsService(t)
	ciphertext := encryptPayload(t, svc, map[string]any{"token": "ghp_realtoken123"})

	row := newMCPRow(t, "mcp-1", "github mcp", []canonical.MCPServer{
		{
			Name:    "github",
			Command: "npx",
			Args:    []string{"-y", "@modelcontextprotocol/server-github"},
			Env: map[string]canonical.EnvValue{
				"GITHUB_TOKEN": {Mode: canonical.EnvModeCredentialRef, CredentialKindCode: "github_pat"},
			},
		},
	}, []store.RequiredCredential{
		{Kind: "github_pat", Required: true},
	})

	c := &Connector{
		capabilities: stubCapabilityStore{
			rows: []store.EnabledCapabilityRead{row},
			credentials: map[string]store.UserCredentialRead{
				"user-1:github_pat": {
					ID:         "uc-1",
					UserID:     "user-1",
					Kind:       "github_pat",
					Ciphertext: ciphertext,
				},
			},
		},
		secrets: svc,
		log:     discardLogger(),
	}
	got, err := c.resolveCapabilityAdditions(context.Background(), defaultPromptInput(), "claude_code")
	if err != nil {
		t.Fatalf("resolveCapabilityAdditions: %v", err)
	}
	srv, ok := got.MCPServers["github"]
	if !ok {
		t.Fatal("missing server 'github'")
	}
	srvMap := srv.(map[string]any)
	env, _ := srvMap["env"].(map[string]string)
	if env["GITHUB_TOKEN"] != "ghp_realtoken123" {
		t.Fatalf("GITHUB_TOKEN = %q, want ghp_realtoken123", env["GITHUB_TOKEN"])
	}
	// ADR-005: per-user credential injection must produce a CredentialEmit
	// tagged so downstream audit emitters can record the source.
	if len(got.CredentialEmits) != 1 {
		t.Fatalf("expected 1 CredentialEmit, got %d", len(got.CredentialEmits))
	}
	emit := got.CredentialEmits[0]
	if emit.InitiatorUserID != "user-1" || emit.CredentialOwnerID != "user-1" {
		t.Fatalf("per-user emit should have initiator==owner==user-1, got initiator=%q owner=%q", emit.InitiatorUserID, emit.CredentialOwnerID)
	}
	if emit.UserCredentialID != "uc-1" || emit.CredentialKind != "github_pat" {
		t.Fatalf("emit id/kind mismatch: %+v", emit)
	}
}

// TestResolveCapabilityAdditions_MCPMissingRequiredCredentialDisables locks
// in the ADR-003 soft-degrade: a missing required credential turns into
// a Disabled entry rather than aborting the prompt. The MCP is excluded
// from MCPServers and the kind is reported so the channel layer can
// nudge the user.
func TestResolveCapabilityAdditions_MCPMissingRequiredCredentialDisables(t *testing.T) {
	row := newMCPRow(t, "mcp-1", "github mcp", []canonical.MCPServer{
		{
			Name:    "github",
			Command: "npx",
			Env: map[string]canonical.EnvValue{
				"GITHUB_TOKEN": {Mode: canonical.EnvModeCredentialRef, CredentialKindCode: "github_pat"},
			},
		},
	}, []store.RequiredCredential{
		{Kind: "github_pat", Required: true},
	})

	svc := testSecretsService(t)
	c := &Connector{
		capabilities: stubCapabilityStore{
			rows:        []store.EnabledCapabilityRead{row},
			credentials: map[string]store.UserCredentialRead{}, // no credentials
		},
		secrets: svc,
		log:     discardLogger(),
	}
	got, err := c.resolveCapabilityAdditions(context.Background(), defaultPromptInput(), "claude_code")
	if err != nil {
		t.Fatalf("expected soft-degrade, got error: %v", err)
	}
	if len(got.MCPServers) != 0 {
		t.Fatalf("MCP should be excluded when its credential is missing, got %d servers", len(got.MCPServers))
	}
	if len(got.Disabled) != 1 {
		t.Fatalf("expected 1 disabled capability, got %d", len(got.Disabled))
	}
	d := got.Disabled[0]
	if d.CapabilityID != row.CapabilityID || d.CapabilityName != row.Name {
		t.Fatalf("disabled descriptor mismatch: %+v", d)
	}
	if len(d.MissingCredentials) != 1 || d.MissingCredentials[0].Kind != "github_pat" {
		t.Fatalf("missing list mismatch: %+v", d.MissingCredentials)
	}
}

func TestResolveCapabilityAdditions_MCPEmptyInitiatorIDErrors(t *testing.T) {
	row := newMCPRow(t, "mcp-1", "github mcp", []canonical.MCPServer{
		{Name: "github", Command: "npx"},
	}, []store.RequiredCredential{
		{Kind: "github_pat", Required: true},
	})

	svc := testSecretsService(t)
	c := &Connector{
		capabilities: stubCapabilityStore{rows: []store.EnabledCapabilityRead{row}},
		secrets:      svc,
		log:          discardLogger(),
	}
	in := defaultPromptInput()
	in.ConversationInitiatorID = "" // empty
	_, err := c.resolveCapabilityAdditions(context.Background(), in, "claude_code")
	if err == nil {
		t.Fatal("expected error when conversation_initiator_id is empty")
	}
}

func TestResolveCapabilityAdditions_MCPNilSecretsErrors(t *testing.T) {
	row := newMCPRow(t, "mcp-1", "github mcp", []canonical.MCPServer{
		{Name: "github", Command: "npx"},
	}, []store.RequiredCredential{
		{Kind: "github_pat", Required: true},
	})

	c := &Connector{
		capabilities: stubCapabilityStore{rows: []store.EnabledCapabilityRead{row}},
		// secrets is nil
		log: discardLogger(),
	}
	_, err := c.resolveCapabilityAdditions(context.Background(), defaultPromptInput(), "claude_code")
	if err == nil {
		t.Fatal("expected error when secrets service is nil")
	}
}

func TestResolveCapabilityAdditions_MixedSkillAndMCP(t *testing.T) {
	skillRow := newSkillRow(t, "s1", "Code Style", "Follow go conventions.")
	mcpRow := newMCPRow(t, "mcp-1", "github mcp", []canonical.MCPServer{
		{Name: "github", Command: "npx", Args: []string{"-y", "gh-server"}},
	}, nil)

	c := &Connector{
		capabilities: stubCapabilityStore{
			rows: []store.EnabledCapabilityRead{skillRow, mcpRow},
		},
		oss: &stubPluginPresigner{},
		log: discardLogger(),
	}
	got, err := c.resolveCapabilityAdditions(context.Background(), defaultPromptInput(), "claude_code")
	if err != nil {
		t.Fatalf("resolveCapabilityAdditions: %v", err)
	}
	if len(got.Skills) != 1 || got.Skills[0].Name != "skill-s1" {
		t.Fatalf("skills = %+v", got.Skills)
	}
	if len(got.MCPServers) != 1 {
		t.Fatalf("mcp servers = %d, want 1", len(got.MCPServers))
	}
	if _, ok := got.MCPServers["github"]; !ok {
		t.Fatal("missing mcp server 'github'")
	}
}

func TestResolveCapabilityAdditions_MCPLegacyContent(t *testing.T) {
	content := `{"mcpServers":{"legacy-server":{"command":"node","args":["server.js"]}}}`
	row := store.EnabledCapabilityRead{
		CapabilityID: "mcp-legacy",
		Name:         "legacy mcp",
		Type:         "mcp",
		Content:      []byte(content),
	}
	c := &Connector{
		capabilities: stubCapabilityStore{rows: []store.EnabledCapabilityRead{row}},
		log:          discardLogger(),
	}
	got, err := c.resolveCapabilityAdditions(context.Background(), defaultPromptInput(), "claude_code")
	if err != nil {
		t.Fatalf("legacy mcp: %v", err)
	}
	if _, ok := got.MCPServers["legacy-server"]; !ok {
		t.Fatalf("expected legacy-server in MCPServers, got %v", got.MCPServers)
	}
}

// ---------------------------------------------------------------------------
// mergeSkillsIntoOptions tests
// ---------------------------------------------------------------------------

func TestMergeSkillsIntoOptions_PopulatesOptsSkills(t *testing.T) {
	opts := map[string]any{}
	mergeSkillsIntoOptions(opts, []ResolvedSkill{
		{Name: "code-review", Version: "1.0.0", DownloadURL: "https://x", SHA256: "aa"},
		{Name: "writer", Version: "2.0.0", DownloadURL: "https://y", SHA256: "bb"},
	})
	got, ok := opts["skills"].([]any)
	if !ok || len(got) != 2 {
		t.Fatalf("opts[skills] = %T (%v), want []any of len 2", opts["skills"], opts["skills"])
	}
	first, _ := got[0].(map[string]any)
	if first["name"] != "code-review" || first["download_url"] != "https://x" {
		t.Fatalf("first entry shape wrong: %+v", first)
	}
}

func TestMergeSkillsIntoOptions_OverrideWins(t *testing.T) {
	// Hand-configured opts["skills"] override capability-resolved list.
	override := []any{map[string]any{"name": "manual"}}
	opts := map[string]any{"skills": override}
	mergeSkillsIntoOptions(opts, []ResolvedSkill{
		{Name: "should-be-dropped", DownloadURL: "x"},
	})
	got, _ := opts["skills"].([]any)
	if len(got) != 1 || got[0].(map[string]any)["name"] != "manual" {
		t.Fatalf("override should win, got %+v", got)
	}
}

func TestMergeSkillsIntoOptions_EmptyInputNoOp(t *testing.T) {
	opts := map[string]any{}
	mergeSkillsIntoOptions(opts, nil)
	if _, ok := opts["skills"]; ok {
		t.Fatalf("empty input should not insert 'skills' key")
	}
}

// ---------------------------------------------------------------------------
// mergeMCPServersIntoOptions tests
// ---------------------------------------------------------------------------

func TestMergeMCPServersIntoOptions_MergesIntoEmpty(t *testing.T) {
	opts := map[string]any{}
	mergeMCPServersIntoOptions(opts, map[string]any{"github": map[string]any{"command": "npx"}})
	mcp, ok := opts["mcp_servers"].(map[string]any)
	if !ok || len(mcp) != 1 {
		t.Fatalf("expected 1 mcp server, got %v", opts["mcp_servers"])
	}
}

func TestMergeMCPServersIntoOptions_StaticWins(t *testing.T) {
	opts := map[string]any{
		"mcp_servers": map[string]any{
			"github": map[string]any{"command": "static-cmd"},
		},
	}
	mergeMCPServersIntoOptions(opts, map[string]any{
		"github": map[string]any{"command": "cap-cmd"},
		"slack":  map[string]any{"command": "slack-cmd"},
	})
	mcp := opts["mcp_servers"].(map[string]any)
	if len(mcp) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(mcp))
	}
	ghCmd := mcp["github"].(map[string]any)["command"]
	if ghCmd != "static-cmd" {
		t.Fatalf("static github should win, got command=%v", ghCmd)
	}
	if _, ok := mcp["slack"]; !ok {
		t.Fatal("capability-resolved 'slack' should be added")
	}
}

func TestMergeMCPServersIntoOptions_NilMapNoop(t *testing.T) {
	opts := map[string]any{"model": "claude"}
	mergeMCPServersIntoOptions(opts, nil)
	if _, ok := opts["mcp_servers"]; ok {
		t.Fatal("should not set mcp_servers when input is nil")
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func stringContains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// emitDisabledCapabilityNotices
// ---------------------------------------------------------------------------

// fakeSystemMessageStore captures the runtime_error system_messages the
// emit produces so tests can assert on the shape without standing up a
// real DB.
type fakeSystemMessageStore struct {
	runtimeErrors   []store.CreateRuntimeErrorSystemMessageInput
	sandboxOfflines []store.CreateSandboxOfflineNoticeInput
	err             error
}

func (f *fakeSystemMessageStore) CreateRuntimeErrorSystemMessage(_ context.Context, input store.CreateRuntimeErrorSystemMessageInput) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.runtimeErrors = append(f.runtimeErrors, input)
	return "msg-" + input.CredentialKind, nil
}

func (f *fakeSystemMessageStore) CreateSandboxOfflineNotice(_ context.Context, input store.CreateSandboxOfflineNoticeInput) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.sandboxOfflines = append(f.sandboxOfflines, input)
	return "sandbox-offline-" + input.DeviceID, nil
}

// TestEmitDisabledCapabilityNotices_PerKindAndPerCapability: one
// notice per (capability, missing kind) pair, with
// CapabilityCredentialMissing SubKind and run / conversation scope
// preserved.
func TestEmitDisabledCapabilityNotices_PerKindAndPerCapability(t *testing.T) {
	sm := &fakeSystemMessageStore{}
	c := &Connector{
		systemMessages: sm,
		log:            discardLogger(),
	}
	in := connector.PromptInput{
		WorkspaceID:    "ws-1",
		AgentID:        "agt-1",
		RunID:          "run-1",
		ConversationID: "conv-1",
	}
	c.emitDisabledCapabilityNotices(context.Background(), in, []DisabledCapability{
		{
			CapabilityID:   "cap-github",
			CapabilityName: "GitHub",
			MissingCredentials: []MissingCredentialRef{
				{Kind: "github_pat"},
				{Kind: "github_oauth"},
			},
		},
		{
			CapabilityID:   "cap-slack",
			CapabilityName: "Slack",
			MissingCredentials: []MissingCredentialRef{
				{Kind: "slack_bot_token"},
			},
		},
	})
	if len(sm.runtimeErrors) != 3 {
		t.Fatalf("expected 3 notices (2 for GitHub + 1 for Slack), got %d: %+v", len(sm.runtimeErrors), sm.runtimeErrors)
	}
	for _, msg := range sm.runtimeErrors {
		if msg.SubKind != CapabilityCredentialMissing {
			t.Errorf("SubKind = %q, want %q", msg.SubKind, CapabilityCredentialMissing)
		}
		if msg.ConversationID != "conv-1" || msg.RunID != "run-1" {
			t.Errorf("notice missing run scope: %+v", msg)
		}
	}
}

// TestEmitDisabledCapabilityNotices_NilStoreWarnsDoesNotPanic guards
// the dev / smoke deployment where SystemMessages is not wired.
// Without the nil guard the connector would panic on first Disabled
// MCP — better to log a WARN and let the run proceed silently than
// take down the dispatch loop. Production main.go MUST wire the
// dependency; the warn-log surfaces misconfiguration.
func TestEmitDisabledCapabilityNotices_NilStoreWarnsDoesNotPanic(t *testing.T) {
	c := &Connector{
		systemMessages: nil,
		log:            discardLogger(),
	}
	// Must not panic.
	c.emitDisabledCapabilityNotices(context.Background(), connector.PromptInput{
		ConversationID: "conv-1",
	}, []DisabledCapability{
		{CapabilityID: "cap-x", CapabilityName: "X", MissingCredentials: []MissingCredentialRef{{Kind: "k"}}},
	})
}

// TestEmitDisabledCapabilityNotices_NoConversationNoOp documents the
// transient / web-chat-without-conversation skip. Without ConversationID
// nobody could surface the notice anyway.
func TestEmitDisabledCapabilityNotices_NoConversationNoOp(t *testing.T) {
	sm := &fakeSystemMessageStore{}
	c := &Connector{
		systemMessages: sm,
		log:            discardLogger(),
	}
	c.emitDisabledCapabilityNotices(context.Background(), connector.PromptInput{ConversationID: ""}, []DisabledCapability{
		{CapabilityID: "cap-x", MissingCredentials: []MissingCredentialRef{{Kind: "k"}}},
	})
	if len(sm.runtimeErrors) != 0 {
		t.Fatalf("expected no notices when conversation is empty, got %d", len(sm.runtimeErrors))
	}
}

// TestEmitDisabledCapabilityNotices_EmptyMissingCredsStillEmits guards
// the defensive branch: a future "disabled for non-credential reason"
// disable should still surface SOMETHING rather than silently dropping
// the user signal.
func TestEmitDisabledCapabilityNotices_EmptyMissingCredsStillEmits(t *testing.T) {
	sm := &fakeSystemMessageStore{}
	c := &Connector{
		systemMessages: sm,
		log:            discardLogger(),
	}
	c.emitDisabledCapabilityNotices(context.Background(), connector.PromptInput{
		ConversationID: "conv-1",
		RunID:          "run-1",
	}, []DisabledCapability{
		{CapabilityID: "cap-x", CapabilityName: "X"},
	})
	if len(sm.runtimeErrors) != 1 {
		t.Fatalf("expected 1 notice for empty-missing disable, got %d", len(sm.runtimeErrors))
	}
	if sm.runtimeErrors[0].CapabilityID != "cap-x" {
		t.Errorf("notice missing capability identity: %+v", sm.runtimeErrors[0])
	}
}
