package dev

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/secrets"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

const (
	testUserAID = "00000000-0000-0000-0000-0000000000aa"
	testUserBID = "00000000-0000-0000-0000-0000000000bb"
)

func TestCapabilityAdminCreatesCapabilityAndVersion(t *testing.T) {
	r, db := capabilityTestRouter(t, map[string]string{store.DefaultDevFixtureIDs().UserID: "admin"}, nil)
	created := serveCapabilityRoute(t, r, http.MethodPost, "/api/v1/workspaces/"+store.DefaultDevFixtureIDs().WorkspaceID+"/capabilities", `{"type":"mcp","name":"GitHub Issues","required_credentials":[{"kind":"github_pat","required":true}],"version":"v1.0.0","content":{"mcpServers":{"github":{"command":"npx"}}}}`, store.DefaultDevFixtureIDs().UserID)
	if created.Code != http.StatusCreated || !strings.Contains(created.Body.String(), "GitHub Issues") {
		t.Fatalf("create capability expected 201, got %d: %s", created.Code, created.Body.String())
	}
	capabilityID := lookupCapabilityID(t, db, "GitHub Issues")
	versioned := serveCapabilityRoute(t, r, http.MethodPost, "/api/v1/workspaces/"+store.DefaultDevFixtureIDs().WorkspaceID+"/capabilities/"+capabilityID+"/versions", `{"version":"v1.1.0","content":{"mcpServers":{"github":{"command":"npx"}}}}`, store.DefaultDevFixtureIDs().UserID)
	if versioned.Code != http.StatusCreated || !strings.Contains(versioned.Body.String(), "v1.1.0") {
		t.Fatalf("create version expected 201, got %d: %s", versioned.Code, versioned.Body.String())
	}
}

func TestCapabilityRejectsUnknownCredentialKind(t *testing.T) {
	r, _ := capabilityTestRouter(t, map[string]string{store.DefaultDevFixtureIDs().UserID: "admin"}, nil)
	res := serveCapabilityRoute(t, r, http.MethodPost, "/api/v1/workspaces/"+store.DefaultDevFixtureIDs().WorkspaceID+"/capabilities", `{"type":"mcp","name":"Unknown Credential","required_credentials":[{"kind":"github_token","required":true}],"version":"v1.0.0","content":{"mcpServers":{"github":{"command":"npx"}}}}`, store.DefaultDevFixtureIDs().UserID)
	if res.Code != http.StatusUnprocessableEntity || !strings.Contains(res.Body.String(), "invalid credential kind") {
		t.Fatalf("unknown capability credential kind expected 422, got %d: %s", res.Code, res.Body.String())
	}
}

func TestCapabilityCreateRequiresWorkspaceAdmin(t *testing.T) {
	r, _ := capabilityTestRouter(t, map[string]string{store.DefaultDevFixtureIDs().UserID: "member"}, nil)
	res := serveCapabilityRoute(t, r, http.MethodPost, "/api/v1/workspaces/"+store.DefaultDevFixtureIDs().WorkspaceID+"/capabilities", `{"type":"mcp","name":"GitHub"}`, store.DefaultDevFixtureIDs().UserID)
	if res.Code != http.StatusForbidden {
		t.Fatalf("non-admin create expected 403, got %d: %s", res.Code, res.Body.String())
	}
}

// TestCapabilityListFiltersByTypeAndName exercises the ?type and ?name query
// params plumbed from the frontend "全部 / MCP / Skill / Plugin" tabs and the
// search box. Backend filter logic lives in store.ListCapabilities; this test
// guards the HTTP layer wiring so a future refactor doesn't silently drop the
// query parameter parsing.
func TestCapabilityListFiltersByTypeAndName(t *testing.T) {
	r, db := capabilityTestRouter(t, map[string]string{store.DefaultDevFixtureIDs().UserID: "admin"}, nil)
	wid := store.DefaultDevFixtureIDs().WorkspaceID
	uid := store.DefaultDevFixtureIDs().UserID
	insertCapabilityOfType(t, db, wid, uid, "mcp", "GitHub MCP filter")
	insertCapabilityOfType(t, db, wid, uid, "skill", "Codereview Skill filter")
	insertCapabilityOfType(t, db, wid, uid, "plugin", "Browser Plugin filter")
	base := "/api/v1/workspaces/" + wid + "/capabilities"
	cases := []struct {
		name     string
		query    string
		mustHave []string
		mustMiss []string
	}{
		{"no filter returns all", "", []string{"GitHub MCP filter", "Codereview Skill filter", "Browser Plugin filter"}, nil},
		{"type=mcp", "?type=mcp", []string{"GitHub MCP filter"}, []string{"Codereview Skill filter", "Browser Plugin filter"}},
		{"type=skill", "?type=skill", []string{"Codereview Skill filter"}, []string{"GitHub MCP filter", "Browser Plugin filter"}},
		{"type=plugin", "?type=plugin", []string{"Browser Plugin filter"}, []string{"GitHub MCP filter", "Codereview Skill filter"}},
		{"name substring case-insensitive", "?name=codereview", []string{"Codereview Skill filter"}, []string{"GitHub MCP filter", "Browser Plugin filter"}},
		{"type and name combined", "?type=mcp&name=github", []string{"GitHub MCP filter"}, []string{"Codereview Skill filter", "Browser Plugin filter"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := serveCapabilityRoute(t, r, http.MethodGet, base+tc.query, "", uid)
			if res.Code != http.StatusOK {
				t.Fatalf("list expected 200, got %d: %s", res.Code, res.Body.String())
			}
			body := res.Body.String()
			for _, want := range tc.mustHave {
				if !strings.Contains(body, want) {
					t.Fatalf("%s: expected response to contain %q, got: %s", tc.name, want, body)
				}
			}
			for _, miss := range tc.mustMiss {
				if strings.Contains(body, miss) {
					t.Fatalf("%s: expected response NOT to contain %q, got: %s", tc.name, miss, body)
				}
			}
		})
	}
}

func TestCapabilityUserCredentialRejectsUnknownKind(t *testing.T) {
	r, _ := capabilityTestRouter(t, nil, map[string]string{testUserAID: "member"})
	res := serveCapabilityRouteWithKey(t, r, http.MethodPost, "/api/v1/me/credentials", `{"kind":"github_token","display_name":"Bad GitHub","plaintext_value":"ghp_bad_secret"}`, testUserAID, "test-master-key-test-master-key-")
	if res.Code != http.StatusUnprocessableEntity || !strings.Contains(res.Body.String(), "invalid credential kind") {
		t.Fatalf("unknown user credential kind expected 422, got %d: %s", res.Code, res.Body.String())
	}
}

func TestCapabilityUserCredentialsOwnScopeAndNoSecretLeak(t *testing.T) {
	masterKey := "test-master-key-test-master-key-"
	r, db := capabilityTestRouter(t, nil, map[string]string{testUserAID: "member", testUserBID: "member"})
	createdA := serveCapabilityRouteWithKey(t, r, http.MethodPost, "/api/v1/me/credentials", `{"kind":"github_pat","display_name":"Alice GitHub","plaintext_value":"ghp_alice_secret"}`, testUserAID, masterKey)
	if createdA.Code != http.StatusCreated || !strings.Contains(createdA.Body.String(), "Alice GitHub") {
		t.Fatalf("create user credential expected 201, got %d: %s", createdA.Code, createdA.Body.String())
	}
	createdB := serveCapabilityRouteWithKey(t, r, http.MethodPost, "/api/v1/me/credentials", `{"kind":"slack_bot_token","display_name":"Bob Slack","plaintext_value":"xoxb-bob-secret"}`, testUserBID, masterKey)
	if createdB.Code != http.StatusCreated {
		t.Fatalf("create B credential expected 201, got %d: %s", createdB.Code, createdB.Body.String())
	}
	credBID := lookupCredentialID(t, db, testUserBID, "slack_bot_token")
	patch := serveCapabilityRouteWithKey(t, r, http.MethodPatch, "/api/v1/me/credentials/"+credBID, `{"display_name":"stolen"}`, testUserAID, masterKey)
	if patch.Code != http.StatusForbidden {
		t.Fatalf("patch B as A expected 403, got %d: %s", patch.Code, patch.Body.String())
	}
	list := serveCapabilityRoute(t, r, http.MethodGet, "/api/v1/me/credentials", "", testUserBID)
	if list.Code != http.StatusOK {
		t.Fatalf("list B credentials expected 200, got %d: %s", list.Code, list.Body.String())
	}
	body := list.Body.String()
	for _, forbidden := range []string{"plaintext", "ciphertext", "xoxb-bob-secret"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("credential response leaked %q: %s", forbidden, body)
		}
	}
}

func TestCapabilityAgentEnableRBACWorkspaceAndUniqueUpdate(t *testing.T) {
	foreignWorkspaceID := "00000000-0000-0000-0000-000000000099"
	r, db := capabilityTestRouter(t, map[string]string{store.DefaultDevFixtureIDs().UserID: "admin", testUserBID: "admin"}, map[string]string{testUserAID: "member", testUserBID: "member"})
	insertForeignWorkspace(t, db, foreignWorkspaceID)
	capID, v1, v2 := insertCapabilityVersions(t, db, store.DefaultDevFixtureIDs().WorkspaceID, "GitHub MCP")
	_ = capID
	foreignCapID, foreignV1, _ := insertCapabilityVersions(t, db, foreignWorkspaceID, "Foreign MCP")
	_ = foreignCapID
	ownedPA := insertProjectAgentForOwner(t, db, testUserAID, "owned-agent")
	otherPA := insertProjectAgentForOwner(t, db, testUserBID, "other-agent")

	enabled := serveCapabilityRoute(t, r, http.MethodPost, "/api/v1/projects/"+store.DefaultDevFixtureIDs().ProjectID+"/agents/"+ownedPA+"/capabilities/"+v1+"/enable", `{}`, testUserAID)
	if enabled.Code != http.StatusOK {
		t.Fatalf("enable own agent expected 200, got %d: %s", enabled.Code, enabled.Body.String())
	}
	forbidden := serveCapabilityRoute(t, r, http.MethodPost, "/api/v1/projects/"+store.DefaultDevFixtureIDs().ProjectID+"/agents/"+otherPA+"/capabilities/"+v1+"/enable", `{}`, testUserAID)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("enable other agent expected 403, got %d: %s", forbidden.Code, forbidden.Body.String())
	}
	foreign := serveCapabilityRoute(t, r, http.MethodPost, "/api/v1/projects/"+store.DefaultDevFixtureIDs().ProjectID+"/agents/"+ownedPA+"/capabilities/"+foreignV1+"/enable", `{}`, testUserAID)
	if foreign.Code != http.StatusForbidden {
		t.Fatalf("unpublished foreign capability expected 403, got %d: %s", foreign.Code, foreign.Body.String())
	}
	updated := serveCapabilityRoute(t, r, http.MethodPost, "/api/v1/projects/"+store.DefaultDevFixtureIDs().ProjectID+"/agents/"+ownedPA+"/capabilities/"+v2+"/enable", `{}`, testUserAID)
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body.String(), v2) {
		t.Fatalf("enable second version expected update 200, got %d: %s", updated.Code, updated.Body.String())
	}
	assertSingleAgentCapability(t, db, ownedPA, capID, v2)
}

func TestCapabilityMarketplacePublishLifecycleSecretCheckAndDeleteRollback(t *testing.T) {
	r, db := capabilityTestRouter(t, map[string]string{store.DefaultDevFixtureIDs().UserID: "admin"}, nil)
	capID, _, _ := insertCapabilityVersions(t, db, store.DefaultDevFixtureIDs().WorkspaceID, "Marketplace Secret")
	if _, err := db.Exec(context.Background(), `update capability_version set content = '{"mcpServers":{"github":{"command":"npx","env":{"GITHUB_PERSONAL_ACCESS_TOKEN":"ghp_123456789012345678901234567890123456"}}}}' where capability_id = $1`, capID); err != nil {
		t.Fatal(err)
	}
	rejected := serveCapabilityRoute(t, r, http.MethodPost, "/api/v1/workspaces/"+store.DefaultDevFixtureIDs().WorkspaceID+"/capabilities/"+capID+"/publish", `{}`, store.DefaultDevFixtureIDs().UserID)
	if rejected.Code != http.StatusBadRequest || !strings.Contains(rejected.Body.String(), "plaintext secret pattern") {
		t.Fatalf("publish with plaintext secret expected 400, got %d: %s", rejected.Code, rejected.Body.String())
	}
	if _, err := db.Exec(context.Background(), `update capability_version set content = '{"mcpServers":{"github":{"command":"npx","env":{"GITHUB_PERSONAL_ACCESS_TOKEN":"${PARSAR_CREDENTIAL:github_pat}"}}}}' where capability_id = $1`, capID); err != nil {
		t.Fatal(err)
	}
	published := serveCapabilityRoute(t, r, http.MethodPost, "/api/v1/workspaces/"+store.DefaultDevFixtureIDs().WorkspaceID+"/capabilities/"+capID+"/publish", `{}`, store.DefaultDevFixtureIDs().UserID)
	if published.Code != http.StatusOK || !strings.Contains(published.Body.String(), `"visibility":"public"`) {
		t.Fatalf("publish expected 200/public, got %d: %s", published.Code, published.Body.String())
	}
	deprecated := serveCapabilityRoute(t, r, http.MethodPost, "/api/v1/workspaces/"+store.DefaultDevFixtureIDs().WorkspaceID+"/capabilities/"+capID+"/deprecate", `{}`, store.DefaultDevFixtureIDs().UserID)
	if deprecated.Code != http.StatusOK || !strings.Contains(deprecated.Body.String(), "deprecated_at") {
		t.Fatalf("deprecate expected timestamp, got %d: %s", deprecated.Code, deprecated.Body.String())
	}
	undeprecated := serveCapabilityRoute(t, r, http.MethodPost, "/api/v1/workspaces/"+store.DefaultDevFixtureIDs().WorkspaceID+"/capabilities/"+capID+"/undeprecate", `{}`, store.DefaultDevFixtureIDs().UserID)
	if undeprecated.Code != http.StatusOK || strings.Contains(undeprecated.Body.String(), "deprecated_at") {
		t.Fatalf("undeprecate expected no deprecated_at, got %d: %s", undeprecated.Code, undeprecated.Body.String())
	}
	unpublished := serveCapabilityRoute(t, r, http.MethodPost, "/api/v1/workspaces/"+store.DefaultDevFixtureIDs().WorkspaceID+"/capabilities/"+capID+"/unpublish", `{}`, store.DefaultDevFixtureIDs().UserID)
	if unpublished.Code != http.StatusOK || !strings.Contains(unpublished.Body.String(), `"visibility":"workspace"`) {
		t.Fatalf("unpublish expected workspace, got %d: %s", unpublished.Code, unpublished.Body.String())
	}
	deleted := serveCapabilityRoute(t, r, http.MethodDelete, "/api/v1/workspaces/"+store.DefaultDevFixtureIDs().WorkspaceID+"/capabilities/"+capID, ``, store.DefaultDevFixtureIDs().UserID)
	if deleted.Code != http.StatusOK || !strings.Contains(deleted.Body.String(), `"deleted_at"`) {
		t.Fatalf("delete expected 200 with deleted_at, got %d: %s", deleted.Code, deleted.Body.String())
	}
}

func TestCapabilityMarketplaceCrossWorkspaceEnableUpgradeUninstallAndReverseQueries(t *testing.T) {
	foreignWorkspaceID := "00000000-0000-0000-0000-000000000099"
	r, db := capabilityTestRouter(t, map[string]string{store.DefaultDevFixtureIDs().UserID: "admin", testUserAID: "admin"}, map[string]string{testUserAID: "member"})
	insertForeignWorkspace(t, db, foreignWorkspaceID)
	capID, v1, v2 := insertCapabilityVersions(t, db, foreignWorkspaceID, "Foreign Public MCP")
	publishForeignCapability(t, db, capID)
	agentA := insertProjectAgentForOwner(t, db, testUserAID, "market-agent-a")
	agentB := insertProjectAgentForOwner(t, db, testUserAID, "market-agent-b")

	enabledA := serveCapabilityRoute(t, r, http.MethodPost, "/api/v1/projects/"+store.DefaultDevFixtureIDs().ProjectID+"/agents/"+agentA+"/capabilities/"+v1+"/enable", `{}`, testUserAID)
	if enabledA.Code != http.StatusOK {
		t.Fatalf("enable marketplace A expected 200, got %d: %s", enabledA.Code, enabledA.Body.String())
	}
	enabledB := serveCapabilityRoute(t, r, http.MethodPost, "/api/v1/projects/"+store.DefaultDevFixtureIDs().ProjectID+"/agents/"+agentB+"/capabilities/"+v1+"/enable", `{}`, testUserAID)
	if enabledB.Code != http.StatusOK {
		t.Fatalf("enable marketplace B expected 200, got %d: %s", enabledB.Code, enabledB.Body.String())
	}
	list := serveCapabilityRoute(t, r, http.MethodGet, "/api/v1/workspaces/"+store.DefaultDevFixtureIDs().WorkspaceID+"/capabilities", ``, testUserAID)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"enabled_agent_count":2`) || !strings.Contains(list.Body.String(), `"from_marketplace":true`) {
		t.Fatalf("reverse marketplace list expected count=2, got %d: %s", list.Code, list.Body.String())
	}
	market := serveCapabilityRoute(t, r, http.MethodGet, "/api/v1/capabilities/marketplace?workspace_id="+store.DefaultDevFixtureIDs().WorkspaceID, ``, testUserAID)
	if market.Code != http.StatusOK || !strings.Contains(market.Body.String(), `"installed":true`) || strings.Contains(market.Body.String(), foreignWorkspaceID) {
		t.Fatalf("marketplace list expected installed without source workspace id leak, got %d: %s", market.Code, market.Body.String())
	}
	upgraded := serveCapabilityRoute(t, r, http.MethodPost, "/api/v1/projects/"+store.DefaultDevFixtureIDs().ProjectID+"/agents/"+agentA+"/capabilities/"+capID+"/upgrade", `{"new_version_id":"`+v2+`"}`, testUserAID)
	if upgraded.Code != http.StatusOK || !strings.Contains(upgraded.Body.String(), v2) {
		t.Fatalf("upgrade expected 200/v2, got %d: %s", upgraded.Code, upgraded.Body.String())
	}
	assertSingleAgentCapability(t, db, agentA, capID, v2)
	if _, err := db.Exec(context.Background(), `update capability set deprecated_at = now() where id = $1`, capID); err != nil {
		t.Fatal(err)
	}
	blocked := serveCapabilityRoute(t, r, http.MethodPost, "/api/v1/projects/"+store.DefaultDevFixtureIDs().ProjectID+"/agents/"+agentB+"/capabilities/"+capID+"/upgrade", `{"new_version_id":"`+v2+`"}`, testUserAID)
	if blocked.Code != http.StatusForbidden {
		t.Fatalf("upgrade deprecated expected 403, got %d: %s", blocked.Code, blocked.Body.String())
	}
	uninstalled := serveCapabilityRoute(t, r, http.MethodPost, "/api/v1/workspaces/"+store.DefaultDevFixtureIDs().WorkspaceID+"/capabilities/uninstall", `{"source_capability_id":"`+capID+`"}`, testUserAID)
	if uninstalled.Code != http.StatusOK || !strings.Contains(uninstalled.Body.String(), `"removed_agent_count":2`) {
		t.Fatalf("uninstall expected remove 2, got %d: %s", uninstalled.Code, uninstalled.Body.String())
	}
}

// TestInstallCountRejectsCrossWorkspace verifies that GET
// /api/v1/workspaces/{workspaceID}/capabilities/{capabilityID}/install-count
// rejects with 404 when the capability does not belong to the URL
// workspace, even if the caller is a legitimate member of that
// workspace. Prevents leaking marketplace install counts of foreign
// workspaces' capabilities (Decision #7 isolation).
func TestInstallCountRejectsCrossWorkspace(t *testing.T) {
	foreignWorkspaceID := "00000000-0000-0000-0000-000000000099"
	r, db := capabilityTestRouter(t, map[string]string{store.DefaultDevFixtureIDs().UserID: "admin", testUserAID: "admin"}, map[string]string{testUserAID: "member"})
	insertForeignWorkspace(t, db, foreignWorkspaceID)
	capID, v1, _ := insertCapabilityVersions(t, db, foreignWorkspaceID, "Foreign Public MCP install-count")
	publishForeignCapability(t, db, capID)
	agentA := insertProjectAgentForOwner(t, db, testUserAID, "market-agent-install-count")
	enabled := serveCapabilityRoute(t, r, http.MethodPost, "/api/v1/projects/"+store.DefaultDevFixtureIDs().ProjectID+"/agents/"+agentA+"/capabilities/"+v1+"/enable", `{}`, testUserAID)
	if enabled.Code != http.StatusOK {
		t.Fatalf("enable marketplace cap expected 200, got %d: %s", enabled.Code, enabled.Body.String())
	}

	// The caller (testUserAID) is admin of DefaultDevFixtureIDs().WorkspaceID
	// but the capability is owned by foreignWorkspaceID. The install-count
	// endpoint MUST reject with 404 — caller cannot pivot through their own
	// workspace URL to read foreign capability metrics.
	leaked := serveCapabilityRoute(t, r, http.MethodGet, "/api/v1/workspaces/"+store.DefaultDevFixtureIDs().WorkspaceID+"/capabilities/"+capID+"/install-count", ``, testUserAID)
	if leaked.Code != http.StatusNotFound {
		t.Fatalf("cross-workspace install-count leak: expected 404, got %d: %s", leaked.Code, leaked.Body.String())
	}

	// Source workspace owner (DefaultDevFixtureIDs().UserID is admin of foreignWorkspaceID
	// via the test setup) querying via foreign workspace URL must work.
	if _, err := db.Exec(context.Background(), `insert into workspace_members(id, workspace_id, user_id, role, created_at, updated_at) values (gen_random_uuid(), $1, $2, 'admin', now(), now())`, foreignWorkspaceID, store.DefaultDevFixtureIDs().UserID); err != nil {
		t.Fatal(err)
	}
	owned := serveCapabilityRoute(t, r, http.MethodGet, "/api/v1/workspaces/"+foreignWorkspaceID+"/capabilities/"+capID+"/install-count", ``, store.DefaultDevFixtureIDs().UserID)
	if owned.Code != http.StatusOK || !strings.Contains(owned.Body.String(), `"install_count":1`) {
		t.Fatalf("source workspace owner install-count expected 200/install_count=1, got %d: %s", owned.Code, owned.Body.String())
	}
}

func capabilityTestRouter(t *testing.T, workspaceRoles map[string]string, _ map[string]string) (http.Handler, *pgxpool.Pool) {
	t.Helper()
	oldKey := os.Getenv("PARSAR_MASTER_KEY")
	os.Setenv("PARSAR_MASTER_KEY", "test-master-key-test-master-key-")
	t.Cleanup(func() { os.Setenv("PARSAR_MASTER_KEY", oldKey) })
	db := openDevRouteTestDB(t)
	s := store.New(db)
	if _, err := s.SeedDevFixture(context.Background()); err != nil {
		t.Fatal(err)
	}
	insertCapabilityExtraUser(t, db, testUserAID, "alice@example.com")
	insertCapabilityExtraUser(t, db, testUserBID, "bob@example.com")
	insertWorkspaceMember(t, db, testUserAID, "member")
	insertWorkspaceMember(t, db, testUserBID, "member")
	r := chi.NewRouter()
	RegisterRoutesWithStore(r, capabilityRBACStore{RuntimeStore: s, workspaceRoles: workspaceRoles})
	return r, db
}

type capabilityRBACStore struct {
	RuntimeStore
	workspaceRoles map[string]string
}

func (s capabilityRBACStore) GetWorkspaceMemberRole(ctx context.Context, workspaceID string, userID string) (string, error) {
	if role, ok := s.workspaceRoles[userID]; ok {
		return role, nil
	}
	return s.RuntimeStore.GetWorkspaceMemberRole(ctx, workspaceID, userID)
}

func serveCapabilityRoute(t *testing.T, r http.Handler, method, path, body, userID string) *httptest.ResponseRecorder {
	return serveCapabilityRouteWithKey(t, r, method, path, body, userID, "test-master-key-test-master-key-")
}

func serveCapabilityRouteWithKey(t *testing.T, r http.Handler, method, path, body, userID, masterKey string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req = req.WithContext(auth.WithUserID(req.Context(), userID))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	return res
}

func lookupCapabilityID(t *testing.T, db *pgxpool.Pool, name string) string {
	t.Helper()
	var id string
	if err := db.QueryRow(context.Background(), `select id::text from capability where name = $1 and deleted_at is null order by created_at desc limit 1`, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func lookupCredentialID(t *testing.T, db *pgxpool.Pool, userID, kind string) string {
	t.Helper()
	var id string
	if err := db.QueryRow(context.Background(), `select id::text from user_credentials where user_id = $1 and kind = $2 and deleted_at is null`, userID, kind).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// insertCapabilityOfType creates a bare capability of the given type
// (mcp / skill / plugin) for ListCapabilities filter testing. No version row
// is created — the list endpoint does not require one.
func insertCapabilityOfType(t *testing.T, db *pgxpool.Pool, workspaceID, userID, capType, name string) string {
	t.Helper()
	var id string
	if err := db.QueryRow(context.Background(),
		`insert into capability(id, workspace_id, type, name, description, visibility, status, creator_id, created_at, updated_at) values (gen_random_uuid(), $1, $2, $3, '', 'workspace', 'active', $4, now(), now()) returning id::text`,
		workspaceID, capType, name, userID,
	).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func insertCapabilityVersions(t *testing.T, db *pgxpool.Pool, workspaceID, name string) (string, string, string) {
	t.Helper()
	var capID, v1, v2 string
	if err := db.QueryRow(context.Background(), `insert into capability(id, workspace_id, type, name, description, visibility, status, creator_id, created_at, updated_at) values (gen_random_uuid(), $1, 'mcp', $2, '', 'workspace', 'active', $3, now(), now()) returning id::text`, workspaceID, name, store.DefaultDevFixtureIDs().UserID).Scan(&capID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(context.Background(), `insert into capability_version(id, capability_id, version, content, required_credentials, creator_id, created_at) values (gen_random_uuid(), $1, 'v1.0.0', '{"mcpServers":{"github":{"command":"npx"}}}', '[{"kind":"github_pat","required":true}]'::jsonb, $2, now()) returning id::text`, capID, store.DefaultDevFixtureIDs().UserID).Scan(&v1); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(context.Background(), `insert into capability_version(id, capability_id, version, content, required_credentials, creator_id, created_at) values (gen_random_uuid(), $1, 'v2.0.0', '{"mcpServers":{"github":{"command":"npx"}}}', '[{"kind":"github_pat","required":true}]'::jsonb, $2, now()) returning id::text`, capID, store.DefaultDevFixtureIDs().UserID).Scan(&v2); err != nil {
		t.Fatal(err)
	}
	return capID, v1, v2
}

func publishForeignCapability(t *testing.T, db *pgxpool.Pool, capabilityID string) {
	t.Helper()
	if _, err := db.Exec(context.Background(), `update capability set visibility = 'public', deprecated_at = null, status = 'active', deleted_at = null where id = $1`, capabilityID); err != nil {
		t.Fatal(err)
	}
}

func insertProjectAgentForOwner(t *testing.T, db *pgxpool.Pool, userID, slug string) string {
	t.Helper()
	var agentID, projectAgentID string
	if err := db.QueryRow(context.Background(), `insert into agents(id, workspace_id, name, slug, connector_type, status, config, created_by, created_at, updated_at) values (gen_random_uuid(), $1, $2, $2, 'agent_daemon', 'active', '{}', $3, now(), now()) returning id::text`, store.DefaultDevFixtureIDs().WorkspaceID, slug, userID).Scan(&agentID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(context.Background(), `insert into project_agents(id, workspace_id, project_id, agent_id, status, config, created_by, created_at, updated_at) values (gen_random_uuid(), $1, $2, $3, 'active', '{"daemon_mode":"sandbox","agent_kind":"opencode"}'::jsonb, $4, now(), now()) returning id::text`, store.DefaultDevFixtureIDs().WorkspaceID, store.DefaultDevFixtureIDs().ProjectID, agentID, userID).Scan(&projectAgentID); err != nil {
		t.Fatal(err)
	}
	return projectAgentID
}

func assertSingleAgentCapability(t *testing.T, db *pgxpool.Pool, projectAgentID, capabilityID, versionID string) {
	t.Helper()
	var count int
	var gotVersion string
	if err := db.QueryRow(context.Background(), `select count(*), max(capability_version_id::text) from agent_capabilities where project_agent_id = $1 and capability_id = $2`, projectAgentID, capabilityID).Scan(&count, &gotVersion); err != nil {
		t.Fatal(err)
	}
	if count != 1 || gotVersion != versionID {
		t.Fatalf("agent capability count/version = %d/%s, want 1/%s", count, gotVersion, versionID)
	}
}

func insertCapabilityExtraUser(t *testing.T, db *pgxpool.Pool, userID, email string) {
	t.Helper()
	if _, err := db.Exec(context.Background(), `insert into users(id, email, name, status, created_at, updated_at) values ($1, $2, $2, 'active', now(), now()) on conflict (id) do nothing`, userID, email); err != nil {
		t.Fatal(err)
	}
}

func insertForeignWorkspace(t *testing.T, db *pgxpool.Pool, workspaceID string) {
	t.Helper()
	if _, err := db.Exec(context.Background(), `insert into workspaces(id, name, slug, created_by, created_at, updated_at) values ($1, 'Foreign', 'foreign', $2, now(), now()) on conflict (id) do nothing`, workspaceID, store.DefaultDevFixtureIDs().UserID); err != nil {
		t.Fatal(err)
	}
}

func insertWorkspaceMember(t *testing.T, db *pgxpool.Pool, userID, role string) {
	t.Helper()
	if _, err := db.Exec(context.Background(), `insert into workspace_members(id, workspace_id, user_id, role, created_at, updated_at) values (gen_random_uuid(), $1, $2, $3, now(), now()) on conflict do nothing`, store.DefaultDevFixtureIDs().WorkspaceID, userID, role); err != nil {
		t.Fatal(err)
	}
}

func TestCapabilityCredentialEncryptionPayloadShape(t *testing.T) {
	svc, err := secrets.New("test-master-key-test-master-key-")
	if err != nil {
		t.Fatal(err)
	}
	encrypted, err := svc.Encrypt(map[string]any{"value": "secret"})
	if err != nil || len(encrypted) == 0 {
		t.Fatalf("encrypt value: len=%d err=%v", len(encrypted), err)
	}
	decrypted, err := svc.Decrypt(encrypted)
	if err != nil {
		t.Fatalf("decrypt value: %v", err)
	}
	if decrypted["value"] != "secret" {
		t.Fatalf("decrypted value = %#v, want secret", decrypted["value"])
	}
}

// TestCapabilityListHidesDeprecatedByDefault is the load-bearing test
// for the picker flow: an admin has soft-removed a capability via
// `/deprecate`, and the frontend `agents.create` capability picker
// (data source = ListCapabilities) MUST stop offering it for new
// bindings. The legacy `status='disabled'` kill-switch is gone; the
// only stop-selling signal now is deprecated_at.
//
// Pairs with TestCapabilityListIncludeDeprecatedSurfacesAll which
// proves the IncludeDeprecated escape hatch still works for the
// reconcile path.
func TestCapabilityListHidesDeprecatedByDefault(t *testing.T) {
	r, db := capabilityTestRouter(t, map[string]string{store.DefaultDevFixtureIDs().UserID: "admin"}, nil)
	wid := store.DefaultDevFixtureIDs().WorkspaceID
	uid := store.DefaultDevFixtureIDs().UserID

	keepID := insertCapabilityOfType(t, db, wid, uid, "mcp", "Active MCP keep")
	hideID := insertCapabilityOfType(t, db, wid, uid, "mcp", "Active MCP to deprecate")
	if _, err := db.Exec(context.Background(), `update capability set deprecated_at = now() where id = $1`, hideID); err != nil {
		t.Fatalf("set deprecated_at: %v", err)
	}

	res := serveCapabilityRoute(t, r, http.MethodGet, "/api/v1/workspaces/"+wid+"/capabilities", "", uid)
	if res.Code != http.StatusOK {
		t.Fatalf("list expected 200, got %d: %s", res.Code, res.Body.String())
	}
	body := res.Body.String()
	if !strings.Contains(body, keepID) {
		t.Fatalf("expected active capability id %s in response, body: %s", keepID, body)
	}
	if strings.Contains(body, hideID) {
		t.Fatalf("deprecated capability id %s must be hidden by default; body: %s", hideID, body)
	}
}

// TestCapabilityListIncludeDeprecatedSurfacesAllInStoreCall verifies
// the store-layer escape hatch: callers that pass IncludeDeprecated=true
// (notably syncAgentCapabilities, which must NOT silently drop deprecated
// bindings when the agent is saved unchanged) get the full row set.
// HTTP layer test is intentionally narrow — we only guard the picker
// path there; this test pins the store contract directly so the
// reconcile loop has a stable invariant to lean on.
func TestCapabilityListIncludeDeprecatedSurfacesAllInStoreCall(t *testing.T) {
	databaseURL := os.Getenv("PARSAR_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("PARSAR_TEST_DATABASE_URL is not set")
	}
	_, db := capabilityTestRouter(t, map[string]string{store.DefaultDevFixtureIDs().UserID: "admin"}, nil)
	wid := store.DefaultDevFixtureIDs().WorkspaceID
	uid := store.DefaultDevFixtureIDs().UserID

	keepID := insertCapabilityOfType(t, db, wid, uid, "mcp", "Reconcile MCP active")
	deprID := insertCapabilityOfType(t, db, wid, uid, "mcp", "Reconcile MCP deprecated")
	if _, err := db.Exec(context.Background(), `update capability set deprecated_at = now() where id = $1`, deprID); err != nil {
		t.Fatalf("set deprecated_at: %v", err)
	}

	s := store.New(db)

	// Default filter: deprecated row must NOT come back.
	def, err := s.ListCapabilities(context.Background(), wid, store.ListCapabilityFilter{})
	if err != nil {
		t.Fatalf("list default: %v", err)
	}
	if !containsCapabilityByID(def, keepID) {
		t.Fatalf("default list missing active id %s", keepID)
	}
	if containsCapabilityByID(def, deprID) {
		t.Fatalf("default list must hide deprecated id %s", deprID)
	}

	// IncludeDeprecated=true: both rows present. This is the contract
	// syncAgentCapabilities depends on to avoid silent unbind on save.
	all, err := s.ListCapabilities(context.Background(), wid, store.ListCapabilityFilter{IncludeDeprecated: true})
	if err != nil {
		t.Fatalf("list include-deprecated: %v", err)
	}
	if !containsCapabilityByID(all, keepID) || !containsCapabilityByID(all, deprID) {
		t.Fatalf("include-deprecated list must contain both ids; active=%v deprecated=%v",
			containsCapabilityByID(all, keepID), containsCapabilityByID(all, deprID))
	}
}

// TestSyncAgentCapabilitiesPreservesDeprecatedBindings is the
// regression test for the silent-delete bug:
//
//   - agent A binds capability C
//   - admin deprecates C
//   - user opens A in the edit dialog and clicks Save without changing
//     anything; the form re-submits capabilities=[C]
//   - syncAgentCapabilities must NOT delete the (A,C) binding just
//     because ListCapabilities now hides C from the default catalog
//
// Without the IncludeDeprecated=true wiring in syncAgentCapabilities,
// the binding gets dropped silently on save and the runtime stops
// finding C — the user sees no error, just a capability that "vanished".
func TestSyncAgentCapabilitiesPreservesDeprecatedBindings(t *testing.T) {
	_, db := capabilityTestRouter(t, map[string]string{store.DefaultDevFixtureIDs().UserID: "admin"}, nil)
	wid := store.DefaultDevFixtureIDs().WorkspaceID
	uid := store.DefaultDevFixtureIDs().UserID

	// Seed capability + a version + an agent + bind the capability.
	capID, v1, _ := insertCapabilityVersions(t, db, wid, "Sync Preserve MCP")
	agentID, projectAgentID := insertProjectAgentForSyncTest(t, db, wid, uid, "sync-preserve-target")
	insertAgentCapability(t, db, projectAgentID, capID, v1)

	// Admin deprecates the capability AFTER it's already bound.
	if _, err := db.Exec(context.Background(), `update capability set deprecated_at = now() where id = $1`, capID); err != nil {
		t.Fatalf("set deprecated_at: %v", err)
	}

	// Simulate the edit-dialog Save: user re-submits the same capability
	// name list (which still contains the now-deprecated cap).
	s := store.New(db)
	if err := syncAgentCapabilities(context.Background(), s, wid, projectAgentID, []string{"Sync Preserve MCP"}); err != nil {
		t.Fatalf("sync: %v", err)
	}

	// Binding must still exist.
	bindings, err := s.ListAgentCapabilities(context.Background(), projectAgentID)
	if err != nil {
		t.Fatalf("list bindings: %v", err)
	}
	found := false
	for _, b := range bindings {
		if b.CapabilityID == capID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("deprecated capability binding was silently dropped (agentID=%s capID=%s); bindings=%+v", agentID, capID, bindings)
	}
}

func containsCapabilityByID(caps []store.CapabilityRead, id string) bool {
	for _, c := range caps {
		if c.ID == id {
			return true
		}
	}
	return false
}

func insertProjectAgentForSyncTest(t *testing.T, db *pgxpool.Pool, workspaceID, userID, name string) (string, string) {
	t.Helper()
	var agentID, projectAgentID string
	if err := db.QueryRow(context.Background(),
		`insert into agents(id, workspace_id, name, description, connector_type, config, created_by, created_at, updated_at)
		 values (gen_random_uuid(), $1, $2, '', 'agent_daemon', '{}'::jsonb, $3, now(), now())
		 returning id::text`,
		workspaceID, name, userID,
	).Scan(&agentID); err != nil {
		t.Fatalf("insert agent: %v", err)
	}
	projectID := store.DefaultDevFixtureIDs().ProjectID
	if err := db.QueryRow(context.Background(),
		`insert into project_agents(id, workspace_id, project_id, agent_id, status, config, created_by, created_at, updated_at)
		 values (gen_random_uuid(), $1, $2, $3, 'active', '{}'::jsonb, $4, now(), now())
		 returning id::text`,
		workspaceID, projectID, agentID, userID,
	).Scan(&projectAgentID); err != nil {
		t.Fatalf("insert project_agent: %v", err)
	}
	return agentID, projectAgentID
}

func insertAgentCapability(t *testing.T, db *pgxpool.Pool, projectAgentID, capabilityID, capabilityVersionID string) {
	t.Helper()
	if _, err := db.Exec(context.Background(),
		`insert into agent_capabilities(id, project_agent_id, capability_id, capability_version_id, enabled, configuration, created_at, updated_at)
		 values (gen_random_uuid(), $1, $2, $3, true, '{}'::jsonb, now(), now())`,
		projectAgentID, capabilityID, capabilityVersionID,
	); err != nil {
		t.Fatalf("insert agent_capability: %v", err)
	}
}

// TestSyncAgentCapabilitiesBindsMarketplaceByName covers the edit-dialog
// path where the user checks a marketplace capability that isn't installed
// in their workspace yet. The agent payload only carries names — so
// syncAgentCapabilities has to resolve the name against the marketplace
// pool, not just ListCapabilities of the local workspace. Without this
// the checkbox would appear to succeed in the UI but silently no-op
// on save.
func TestSyncAgentCapabilitiesBindsMarketplaceByName(t *testing.T) {
	foreignWorkspaceID := "00000000-0000-0000-0000-000000000099"
	_, db := capabilityTestRouter(t, map[string]string{store.DefaultDevFixtureIDs().UserID: "admin"}, nil)
	insertForeignWorkspace(t, db, foreignWorkspaceID)
	capID, v1, _ := insertCapabilityVersions(t, db, foreignWorkspaceID, "Foreign Marketplace MCP")
	publishForeignCapability(t, db, capID)

	wid := store.DefaultDevFixtureIDs().WorkspaceID
	uid := store.DefaultDevFixtureIDs().UserID
	_, projectAgentID := insertProjectAgentForSyncTest(t, db, wid, uid, "sync-marketplace-target")

	s := store.New(db)
	if err := syncAgentCapabilities(context.Background(), s, wid, projectAgentID, []string{"Foreign Marketplace MCP"}); err != nil {
		t.Fatalf("sync: %v", err)
	}

	bindings, err := s.ListAgentCapabilities(context.Background(), projectAgentID)
	if err != nil {
		t.Fatalf("list bindings: %v", err)
	}
	var matched store.AgentCapabilityRead
	for _, b := range bindings {
		if b.CapabilityID == capID {
			matched = b
			break
		}
	}
	if matched.CapabilityID == "" {
		t.Fatalf("marketplace capability binding was dropped silently (capID=%s); bindings=%+v", capID, bindings)
	}
	if matched.CapabilityVersionID != v1 {
		t.Fatalf("expected binding to source version %s, got %s", v1, matched.CapabilityVersionID)
	}
}

// TestCapabilityDeleteFreesUniqueNameSlot 复现 bug:被"废弃"的能力名字仍占
// 着唯一索引;只有真正 delete(写 deleted_at)才能让同名重新导入成功。
// 链路:create -> deprecate -> 同名 import 仍冲突 -> delete -> 同名 import 成功。
func TestCapabilityDeleteFreesUniqueNameSlot(t *testing.T) {
	r, db := capabilityTestRouter(t, map[string]string{store.DefaultDevFixtureIDs().UserID: "admin"}, nil)
	wid := store.DefaultDevFixtureIDs().WorkspaceID
	uid := store.DefaultDevFixtureIDs().UserID
	capID, _, _ := insertCapabilityVersions(t, db, wid, "Reusable Name")

	// Deprecate the existing capability. The list view hides it but the
	// unique index (WHERE deleted_at IS NULL) still reserves the name.
	dep := serveCapabilityRoute(t, r, http.MethodPost, "/api/v1/workspaces/"+wid+"/capabilities/"+capID+"/deprecate", `{}`, uid)
	if dep.Code != http.StatusOK {
		t.Fatalf("deprecate expected 200, got %d: %s", dep.Code, dep.Body.String())
	}

	// Attempting to import the same name should still 409 — deprecate is
	// "author retirement", not "freed name".
	reImportBody := `{"kind":"skill","raw_text":"---\nname: Reusable Name\ndescription: |\n  A skill description that is at least twenty characters.\n---\n# Body","source_format":"markdown"}`
	previewBlocked := serveCapabilityRoute(t, r, http.MethodPost, "/api/v1/workspaces/"+wid+"/capabilities/import/preview", reImportBody, uid)
	if previewBlocked.Code != http.StatusOK {
		t.Fatalf("preview expected 200 (preview is just parse, no DB check), got %d: %s", previewBlocked.Code, previewBlocked.Body.String())
	}
	commitBody := `{"kind":"skill","name":"Reusable Name","description":"d","visibility":"workspace","type":"skill","canonical_spec":{"kind":"skill","schema_version":"1","skill":{"name":"Reusable Name","description":"A skill description that is at least twenty characters.","slug":"reusable-name","instructions":"# Body","source_format":"markdown"}}}`
	commitBlocked := serveCapabilityRoute(t, r, http.MethodPost, "/api/v1/workspaces/"+wid+"/capabilities/import/commit", commitBody, uid)
	if commitBlocked.Code != http.StatusConflict {
		t.Fatalf("commit-while-deprecated expected 409, got %d: %s", commitBlocked.Code, commitBlocked.Body.String())
	}

	// Now actually delete. unique index frees the slot.
	del := serveCapabilityRoute(t, r, http.MethodDelete, "/api/v1/workspaces/"+wid+"/capabilities/"+capID, ``, uid)
	if del.Code != http.StatusOK || !strings.Contains(del.Body.String(), `"deleted_at"`) {
		t.Fatalf("delete expected 200 + deleted_at, got %d: %s", del.Code, del.Body.String())
	}

	// Re-import — same name now works.
	commitOK := serveCapabilityRoute(t, r, http.MethodPost, "/api/v1/workspaces/"+wid+"/capabilities/import/commit", commitBody, uid)
	if commitOK.Code != http.StatusCreated {
		t.Fatalf("commit-after-delete expected 201, got %d: %s", commitOK.Code, commitOK.Body.String())
	}
}

// TestCapabilityDeleteRejectedWhenAgentBound covers the 409 path: capability
// is still wired into an agent_capabilities row, so a delete would leave that
// agent referencing a vanished capability.
func TestCapabilityDeleteRejectedWhenAgentBound(t *testing.T) {
	r, db := capabilityTestRouter(t, map[string]string{store.DefaultDevFixtureIDs().UserID: "admin"}, nil)
	wid := store.DefaultDevFixtureIDs().WorkspaceID
	uid := store.DefaultDevFixtureIDs().UserID
	capID, v1, _ := insertCapabilityVersions(t, db, wid, "Bound Capability")
	_, projectAgentID := insertProjectAgentForSyncTest(t, db, wid, uid, "Agent Holding Cap")
	insertAgentCapability(t, db, projectAgentID, capID, v1)

	del := serveCapabilityRoute(t, r, http.MethodDelete, "/api/v1/workspaces/"+wid+"/capabilities/"+capID, ``, uid)
	if del.Code != http.StatusConflict {
		t.Fatalf("delete-while-bound expected 409, got %d: %s", del.Code, del.Body.String())
	}
	var body struct {
		Error        string `json:"error"`
		Message      string `json:"message"`
		BindingCount int64  `json:"binding_count"`
	}
	if err := json.Unmarshal(del.Body.Bytes(), &body); err != nil {
		t.Fatalf("409 body not valid json: %v / %s", err, del.Body.String())
	}
	// Envelope shape must match the project convention { error: <code>, message:
	// <human text>, ... }. The frontend ApiError reads `error` as code and
	// `message` as text; the old free-form-sentence-as-code shape would break
	// any code-based branching upstream.
	if body.Error != "capability_in_use" {
		t.Fatalf("409 code should be capability_in_use, got %q (body=%s)", body.Error, del.Body.String())
	}
	if body.BindingCount != 1 {
		t.Fatalf("409 binding_count should be 1 (one agent_capabilities row), got %d", body.BindingCount)
	}
	if body.Message == "" {
		t.Fatalf("409 should carry a human message, got empty (body=%s)", del.Body.String())
	}
	// Guard against the misleading "让作者标记下架" suggestion — the whole point
	// of this MR is that deprecate does NOT free the name slot, so the 409
	// message must not nudge users in that direction.
	if strings.Contains(body.Message, "标记下架") {
		t.Fatalf("409 message must not suggest deprecate (which does not free the name slot), got: %s", body.Message)
	}

	// Capability must still be alive — UPDATE 0 rows means nothing was written.
	get := serveCapabilityRoute(t, r, http.MethodGet, "/api/v1/workspaces/"+wid+"/capabilities/"+capID, ``, uid)
	if get.Code != http.StatusOK {
		t.Fatalf("after blocked delete capability should still exist, got %d: %s", get.Code, get.Body.String())
	}
	if strings.Contains(get.Body.String(), `"deleted_at":"`) && !strings.Contains(get.Body.String(), `"deleted_at":null`) {
		t.Fatalf("after blocked delete capability must not have deleted_at set, got %s", get.Body.String())
	}
}
