package skilldirectory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/skillcatalog"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/storage/blob"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

const (
	testWorkspaceID  = "00000000-0000-0000-0000-000000000011"
	testUserID       = "00000000-0000-0000-0000-000000000022"
	testCapabilityID = "00000000-0000-0000-0000-000000000033"
)

type fakeCatalog struct {
	catalog skillcatalog.Catalog
	pkg     skillcatalog.Package
	err     error
}

func (f fakeCatalog) Load() (skillcatalog.Catalog, error) { return f.catalog, f.err }

func (f fakeCatalog) LoadItem(id string) (skillcatalog.Item, skillcatalog.Package, error) {
	item, found := f.catalog.Find(id)
	if !found {
		return skillcatalog.Item{}, skillcatalog.Package{}, skillcatalog.ErrItemNotFound
	}
	return item, f.pkg, nil
}

type fakeDirectoryStore struct {
	role      string
	roleErr   error
	installs  []store.CapabilityDirectoryInstall
	listErr   error
	importErr error
	imported  *store.ImportCapabilityInput
}

func (f *fakeDirectoryStore) GetWorkspaceMemberRole(context.Context, string, string) (string, error) {
	if f.roleErr != nil {
		return "", f.roleErr
	}
	return f.role, nil
}

func (f *fakeDirectoryStore) ListCapabilityDirectoryInstalls(context.Context, string, string, string) ([]store.CapabilityDirectoryInstall, error) {
	return append([]store.CapabilityDirectoryInstall(nil), f.installs...), f.listErr
}

func (f *fakeDirectoryStore) ImportCapability(_ context.Context, input store.ImportCapabilityInput) (store.ImportCapabilityResult, error) {
	f.imported = &input
	if f.importErr != nil {
		return store.ImportCapabilityResult{}, f.importErr
	}
	f.installs = append(f.installs, store.CapabilityDirectoryInstall{CatalogID: "frontend-design", CapabilityID: testCapabilityID})
	return store.ImportCapabilityResult{Capability: store.CapabilityRead{ID: testCapabilityID, Name: input.Name, Type: input.Type}}, nil
}

func TestDirectoryListAllowsMembersAndReportsInstallation(t *testing.T) {
	fs := &fakeDirectoryStore{
		role:     "member",
		installs: []store.CapabilityDirectoryInstall{{CatalogID: "frontend-design", CapabilityID: testCapabilityID}},
	}
	rec := request(t, fs, http.MethodGet, "/api/v1/workspaces/"+testWorkspaceID+"/skill-directory")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response listResponse
	decodeResponse(t, rec, &response)
	if len(response.Items) != 1 || !response.Items[0].Installed || response.Items[0].InstalledCapabilityID == nil || *response.Items[0].InstalledCapabilityID != testCapabilityID {
		t.Fatalf("response=%+v", response)
	}
}

func TestDirectoryImportRejectsViewer(t *testing.T) {
	fs := &fakeDirectoryStore{role: "viewer"}
	bs := blob.NewMemoryStore("http://test")
	rec := requestWithDeps(t, fs, bs, http.MethodPost, "/api/v1/workspaces/"+testWorkspaceID+"/skill-directory/frontend-design/import")
	if rec.Code != http.StatusForbidden || fs.imported != nil {
		t.Fatalf("status=%d imported=%v body=%s", rec.Code, fs.imported != nil, rec.Body.String())
	}
}

func TestDirectoryDetailReturnsCompleteSkillContent(t *testing.T) {
	fs := &fakeDirectoryStore{role: "member"}
	bs := blob.NewMemoryStore("http://test")
	rec := requestWithDeps(t, fs, bs, http.MethodGet, "/api/v1/workspaces/"+testWorkspaceID+"/skill-directory/frontend-design")
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response itemResponse
	decodeResponse(t, rec, &response)
	if response.Slug != "frontend-design" || response.Instruction != "Use deliberate visual direction." || len(response.Files) != 1 || response.Files[0].Path != "references/patterns.md" {
		t.Fatalf("response=%+v", response)
	}
}

func TestDirectoryImportStoresZipAndReusesCapabilityImport(t *testing.T) {
	fs := &fakeDirectoryStore{role: "member"}
	bs := blob.NewMemoryStore("http://test")
	rec := requestWithDeps(t, fs, bs, http.MethodPost, "/api/v1/workspaces/"+testWorkspaceID+"/skill-directory/frontend-design/import")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if fs.imported == nil || fs.imported.Type != "skill" || fs.imported.Visibility != "workspace" || fs.imported.CreatorID != testUserID || len(fs.imported.InlineSecrets) != 0 {
		t.Fatalf("import input=%+v", fs.imported)
	}
	if fs.imported.OssKey == "" || fs.imported.SHA256 == "" {
		t.Fatalf("storage fields missing: %+v", fs.imported)
	}
	stored, err := bs.Download(context.Background(), fs.imported.OssKey)
	if err != nil {
		t.Fatalf("download stored package: %v", err)
	}
	if string(stored) != "skill-zip" {
		t.Fatalf("stored zip=%q", stored)
	}
	var source sourcePayload
	if err := json.Unmarshal(fs.imported.SourcePayload, &source); err != nil {
		t.Fatal(err)
	}
	if source.SourceFormat != sourceFormat || source.CatalogID != "frontend-design" || source.CatalogVersion != "1.0.0" {
		t.Fatalf("source=%+v", source)
	}

	rec = requestWithDeps(t, fs, bs, http.MethodPost, "/api/v1/workspaces/"+testWorkspaceID+"/skill-directory/frontend-design/import")
	if rec.Code != http.StatusOK {
		t.Fatalf("repeat status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDirectoryUnknownItemReturnsNotFound(t *testing.T) {
	fs := &fakeDirectoryStore{role: "member"}
	bs := blob.NewMemoryStore("http://test")
	rec := requestWithDeps(t, fs, bs, http.MethodGet, "/api/v1/workspaces/"+testWorkspaceID+"/skill-directory/missing")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDirectoryImportUnknownItemReturnsNotFound(t *testing.T) {
	fs := &fakeDirectoryStore{role: "member"}
	bs := blob.NewMemoryStore("http://test")
	rec := requestWithDeps(t, fs, bs, http.MethodPost, "/api/v1/workspaces/"+testWorkspaceID+"/skill-directory/missing/import")
	if rec.Code != http.StatusNotFound || fs.imported != nil {
		t.Fatalf("status=%d imported=%v body=%s", rec.Code, fs.imported != nil, rec.Body.String())
	}
}

func request(t *testing.T, fs *fakeDirectoryStore, method, path string) *httptest.ResponseRecorder {
	return requestWithDeps(t, fs, blob.NewMemoryStore("http://test"), method, path)
}

func requestWithDeps(t *testing.T, fs *fakeDirectoryStore, bs blob.Store, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.WithUserID(r.Context(), testUserID)))
		})
	})
	RegisterRoutes(router, Deps{Catalog: testCatalogLoader(), Store: fs, Blobs: bs})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func testCatalogLoader() fakeCatalog {
	zipBytes := []byte("skill-zip")
	sum := sha256.Sum256(zipBytes)
	return fakeCatalog{
		catalog: skillcatalog.Catalog{
			SchemaVersion: 1,
			UpdatedAt:     "2026-08-03T00:00:00Z",
			Items: []skillcatalog.Item{{
				ID: "frontend-design", Name: "Frontend Design", Description: "Design interfaces.",
				Publisher: skillcatalog.Publisher{Name: "Anthropic", URL: "https://github.com/anthropics/skills"},
				Verified:  true, Categories: []string{"Design"}, FeaturedRank: 1, Version: "1.0.0", License: "Apache-2.0",
				SourceRef: "b29e7cf65e5cb78a5ac33d582270551bc74a14eb", SourcePath: "skills/frontend-design", ContentPath: "items/frontend-design",
			}},
		},
		pkg: skillcatalog.Package{
			Spec: canonical.Spec{SchemaVersion: canonical.SchemaVersionCurrent, Kind: canonical.KindSkill, Skill: &canonical.SkillSpec{
				Slug: "frontend-design", Title: "Frontend Design", Instruction: "Use deliberate visual direction.",
				Files: []canonical.SkillFile{{Path: "references/patterns.md", Content: "patterns", Kind: canonical.SkillFileKindMarkdown}},
			}},
			Zip: zipBytes, SHA256: hex.EncodeToString(sum[:]),
		},
	}
}

func decodeResponse(t *testing.T, rec *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), target); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
	}
}
