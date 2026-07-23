package mcpdirectory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/mcpcatalog"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

const (
	testWorkspaceID  = "00000000-0000-0000-0000-000000000011"
	testUserID       = "00000000-0000-0000-0000-000000000022"
	testCapabilityID = "00000000-0000-0000-0000-000000000033"
)

type fakeCatalog struct {
	catalog mcpcatalog.Catalog
	err     error
}

func (f fakeCatalog) Load() (mcpcatalog.Catalog, error) { return f.catalog, f.err }

type fakeDirectoryStore struct {
	role              string
	roleErr           error
	installs          []store.MCPDirectoryInstall
	listErr           error
	importErr         error
	concurrentInstall bool
	imported          *store.ImportCapabilityInput
}

type fakeWorkspaceCredentialStore struct {
	secrets []store.SecretRead
}

func (f *fakeWorkspaceCredentialStore) ListSecrets(context.Context, string, int32) ([]store.SecretRead, error) {
	return append([]store.SecretRead(nil), f.secrets...), nil
}

func (f *fakeWorkspaceCredentialStore) CreateSecret(context.Context, store.CreateSecretInput, []byte) (store.SecretRead, error) {
	return store.SecretRead{}, nil
}

func (f *fakeWorkspaceCredentialStore) UpdateSecretPayload(context.Context, string, string, []byte) (store.SecretPayload, error) {
	return store.SecretPayload{}, nil
}

func (f *fakeDirectoryStore) GetWorkspaceMemberRole(context.Context, string, string) (string, error) {
	if f.roleErr != nil {
		return "", f.roleErr
	}
	return f.role, nil
}

func (f *fakeDirectoryStore) ListMCPDirectoryInstalls(context.Context, string) ([]store.MCPDirectoryInstall, error) {
	return append([]store.MCPDirectoryInstall(nil), f.installs...), f.listErr
}

func (f *fakeDirectoryStore) ImportCapability(_ context.Context, input store.ImportCapabilityInput) (store.ImportCapabilityResult, error) {
	f.imported = &input
	if f.importErr != nil {
		if f.concurrentInstall {
			f.installs = append(f.installs, store.MCPDirectoryInstall{CatalogID: "context7", CapabilityID: testCapabilityID})
		}
		return store.ImportCapabilityResult{}, f.importErr
	}
	f.installs = append(f.installs, store.MCPDirectoryInstall{CatalogID: "context7", CapabilityID: testCapabilityID})
	return store.ImportCapabilityResult{Capability: store.CapabilityRead{ID: testCapabilityID, Name: input.Name, Type: input.Type}}, nil
}

func TestDirectoryReadAllowsWorkspaceMember(t *testing.T) {
	fs := &fakeDirectoryStore{role: "member"}
	rec := request(t, fs, http.MethodGet, "/api/v1/workspaces/"+testWorkspaceID+"/mcp-directory")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response listResponse
	decodeResponse(t, rec, &response)
	if len(response.Items) != 1 || response.Items[0].ID != "context7" {
		t.Fatalf("response=%+v", response)
	}
}

func TestDirectoryImportRejectsViewer(t *testing.T) {
	fs := &fakeDirectoryStore{role: "viewer"}
	rec := request(t, fs, http.MethodPost, "/api/v1/workspaces/"+testWorkspaceID+"/mcp-directory/context7/import")
	if rec.Code != http.StatusForbidden || fs.imported != nil {
		t.Fatalf("status=%d imported=%v body=%s", rec.Code, fs.imported != nil, rec.Body.String())
	}
}

func TestDirectoryImportUsesServerCatalogAndCreatesNoSecretsOrBindings(t *testing.T) {
	for _, role := range []string{"owner", "admin", "member"} {
		t.Run(role, func(t *testing.T) {
			fs := &fakeDirectoryStore{role: role}
			rec := request(t, fs, http.MethodPost, "/api/v1/workspaces/"+testWorkspaceID+"/mcp-directory/context7/import")
			if rec.Code != http.StatusCreated {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			input := fs.imported
			if input == nil || input.Type != "mcp" || input.Visibility != "workspace" || input.CreatorID != testUserID {
				t.Fatalf("input=%+v", input)
			}
			if len(input.InlineSecrets) != 0 {
				t.Fatalf("inline secrets=%+v", input.InlineSecrets)
			}
			if input.Spec.MCP == nil || input.Spec.MCP.Servers[0].URL != "https://mcp.context7.com/mcp" {
				t.Fatalf("spec=%+v", input.Spec)
			}
			var source sourcePayload
			if err := json.Unmarshal(input.SourcePayload, &source); err != nil {
				t.Fatal(err)
			}
			if source.SourceFormat != "mcp_catalog" || source.CatalogID != "context7" || source.CatalogVersion != "1.0.0" {
				t.Fatalf("source=%+v", source)
			}
		})
	}
}

func TestDirectoryImportIsIdempotent(t *testing.T) {
	fs := &fakeDirectoryStore{role: "admin", installs: []store.MCPDirectoryInstall{{CatalogID: "context7", CapabilityID: testCapabilityID}}}
	rec := request(t, fs, http.MethodPost, "/api/v1/workspaces/"+testWorkspaceID+"/mcp-directory/context7/import")
	if rec.Code != http.StatusOK || fs.imported != nil {
		t.Fatalf("status=%d imported=%v body=%s", rec.Code, fs.imported != nil, rec.Body.String())
	}
	var response importResponse
	decodeResponse(t, rec, &response)
	if !response.Installed || response.CapabilityID != testCapabilityID {
		t.Fatalf("response=%+v", response)
	}
}

func TestOAuthDirectoryItemRequiresWorkspaceConnectionBeforeImport(t *testing.T) {
	catalog := testCatalog()
	catalog.Items = []mcpcatalog.Item{{
		ID: "notion", Name: "Notion", Description: "Search Notion.",
		Publisher: mcpcatalog.Publisher{Name: "Notion", URL: "https://www.notion.so"},
		Verified:  true, Categories: []string{"Productivity"}, FeaturedRank: 1,
		Version: "1.0.0", Transport: "streamable-http",
		Authentication: mcpcatalog.Authentication{Type: "oauth2"},
		Server:         mcpcatalog.Server{Name: "notion", URL: "https://mcp.notion.com/mcp"},
	}}
	fs := &fakeDirectoryStore{role: "admin"}
	credentials := &fakeWorkspaceCredentialStore{}
	rec := requestWithDeps(t, fs, credentials, catalog, http.MethodPost, "/api/v1/workspaces/"+testWorkspaceID+"/mcp-directory/notion/import")
	if rec.Code != http.StatusConflict || fs.imported != nil {
		t.Fatalf("status=%d imported=%v body=%s", rec.Code, fs.imported != nil, rec.Body.String())
	}

	credentials.secrets = []store.SecretRead{{
		ID: "secret-1", Kind: "capability_inline", Provider: "notion", AuthType: "oauth2", Status: "active",
		Metadata: map[string]any{"workspace_id": testWorkspaceID, "credential_kind_code": "notion_integration"},
	}}
	rec = requestWithDeps(t, fs, credentials, catalog, http.MethodPost, "/api/v1/workspaces/"+testWorkspaceID+"/mcp-directory/notion/import")
	if rec.Code != http.StatusConflict || fs.imported != nil {
		t.Fatalf("legacy Notion token must not satisfy MCP OAuth: status=%d imported=%v body=%s", rec.Code, fs.imported != nil, rec.Body.String())
	}

	credentials.secrets = []store.SecretRead{{
		ID: "secret-2", Kind: "capability_inline", Provider: "notion", AuthType: "oauth2", Status: "active",
		Metadata: map[string]any{"workspace_id": testWorkspaceID, "credential_kind_code": mcpcatalog.OAuthCredentialKind},
	}}
	rec = requestWithDeps(t, fs, credentials, catalog, http.MethodPost, "/api/v1/workspaces/"+testWorkspaceID+"/mcp-directory/notion/import")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	header := fs.imported.Spec.MCP.Servers[0].Headers["Authorization"]
	if header.Prefix != "Bearer " || header.CredentialKindCode != mcpcatalog.OAuthCredentialKind {
		t.Fatalf("authorization header = %+v", header)
	}
}

func TestDirectoryImportRecoversConcurrentIdenticalImport(t *testing.T) {
	fs := &fakeDirectoryStore{role: "admin", importErr: store.ErrCapabilityNameTaken, concurrentInstall: true}
	rec := request(t, fs, http.MethodPost, "/api/v1/workspaces/"+testWorkspaceID+"/mcp-directory/context7/import")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDirectoryUnknownCatalogItem(t *testing.T) {
	fs := &fakeDirectoryStore{role: "member"}
	rec := request(t, fs, http.MethodGet, "/api/v1/workspaces/"+testWorkspaceID+"/mcp-directory/unknown")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDirectoryDetailIncludesStreamableHTTPURL(t *testing.T) {
	fs := &fakeDirectoryStore{role: "member"}
	catalog := testCatalog()
	catalog.Items = []mcpcatalog.Item{{
		ID: "docs", Name: "Docs", Description: "Search docs.",
		Publisher: mcpcatalog.Publisher{Name: "Publisher", URL: "https://example.com"},
		Verified:  true, Categories: []string{"Documentation"}, FeaturedRank: 1,
		Version: "1.0.0", Transport: "streamable-http",
		Server: mcpcatalog.Server{Name: "docs", URL: "https://docs.example.com/mcp"},
	}}
	rec := requestWithCatalog(t, fs, catalog, http.MethodGet, "/api/v1/workspaces/"+testWorkspaceID+"/mcp-directory/docs")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response itemResponse
	decodeResponse(t, rec, &response)
	if response.Transport != "streamable-http" || response.URL != "https://docs.example.com/mcp" {
		t.Fatalf("response=%+v", response)
	}
}

func TestDirectoryRejectsNonMember(t *testing.T) {
	fs := &fakeDirectoryStore{roleErr: store.ErrNotMember}
	rec := request(t, fs, http.MethodGet, "/api/v1/workspaces/"+testWorkspaceID+"/mcp-directory")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDirectoryRejectsInvalidWorkspaceID(t *testing.T) {
	fs := &fakeDirectoryStore{role: "member"}
	rec := request(t, fs, http.MethodGet, "/api/v1/workspaces/not-a-uuid/mcp-directory")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func request(t *testing.T, fs *fakeDirectoryStore, method, path string) *httptest.ResponseRecorder {
	return requestWithCatalog(t, fs, testCatalog(), method, path)
}

func requestWithCatalog(t *testing.T, fs *fakeDirectoryStore, catalog mcpcatalog.Catalog, method, path string) *httptest.ResponseRecorder {
	return requestWithDeps(t, fs, nil, catalog, method, path)
}

func requestWithDeps(t *testing.T, fs *fakeDirectoryStore, credentials workspaceCredentialStore, catalog mcpcatalog.Catalog, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.WithUserID(r.Context(), testUserID)))
		})
	})
	RegisterRoutes(router, Deps{Catalog: fakeCatalog{catalog: catalog}, Store: fs, WorkspaceCredentials: credentials})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func testCatalog() mcpcatalog.Catalog {
	return mcpcatalog.Catalog{
		SchemaVersion: 1,
		UpdatedAt:     "2026-07-22T00:00:00Z",
		Items: []mcpcatalog.Item{{
			ID: "context7", Name: "Context7", Description: "Search current documentation.",
			Publisher: mcpcatalog.Publisher{Name: "MCP", URL: "https://example.com"},
			Verified:  true, Categories: []string{"Documentation"}, FeaturedRank: 1,
			Version: "1.0.0", Transport: "streamable-http",
			Server: mcpcatalog.Server{Name: "context7", URL: "https://mcp.context7.com/mcp"},
		}},
	}
}

func decodeResponse(t *testing.T, rec *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), target); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
	}
}
