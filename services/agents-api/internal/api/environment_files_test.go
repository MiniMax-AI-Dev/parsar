package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

type environmentFilesFixture struct {
	ResourceStore
	environment           store.Environment
	result                proto.WorkspaceDirectoryResult
	storeError, readError error
	lookups, reads        int
	directory             string
	readEnvironment       store.Environment
	readDelay             time.Duration
}

func (f *environmentFilesFixture) GetEnvironment(_ context.Context, tenant, id string) (store.Environment, error) {
	f.lookups++
	if f.storeError != nil {
		return store.Environment{}, f.storeError
	}
	if tenant != f.environment.TenantID || id != f.environment.ID {
		return store.Environment{}, store.ErrNotFound
	}
	return f.environment, nil
}

func (f *environmentFilesFixture) ReadEnvironmentDirectory(ctx context.Context, environment store.Environment, directory string) (proto.WorkspaceDirectoryResult, error) {
	f.reads++
	f.directory, f.readEnvironment = directory, environment
	if f.readDelay > 0 {
		select {
		case <-time.After(f.readDelay):
		case <-ctx.Done():
			return proto.WorkspaceDirectoryResult{}, ctx.Err()
		}
	}
	return f.result, f.readError
}

func environmentFilesHandler(t *testing.T, enabled bool) (http.Handler, *environmentFilesFixture) {
	t.Helper()
	f := &environmentFilesFixture{
		environment: store.Environment{ID: uuid.NewString(), TenantID: uuid.NewString(), SessionID: uuid.NewString(), Status: "connected",
			Configuration: json.RawMessage(`{"type":"self_hosted","workspace_directory":"/private/workspace"}`)},
		result: proto.WorkspaceDirectoryResult{Entries: []proto.WorkspaceDirectoryEntry{}},
	}
	keys := []APIKey{}
	for _, key := range []struct{ token, tenant, project string }{
		{"files-key", f.environment.TenantID, "files-project"},
		{"shared-key", f.environment.TenantID, "files-project"},
		{"other-key", uuid.NewString(), "other-project"},
	} {
		keys = append(keys, APIKey{OrganizationID: "files-org", ProjectID: key.project, SubjectKind: "user", SubjectID: key.token,
			TokenSHA256: device.HashCredential(key.token), TenantID: key.tenant})
	}
	auth, err := NewAuthenticator(keys)
	if err != nil {
		t.Fatal(err)
	}
	options := []Option{}
	if enabled {
		options = append(options, WithEnvironmentDirectoryReader(f))
	}
	h, err := NewHandler(f, auth, "claude_code", options...)
	if err != nil {
		t.Fatal(err)
	}
	return h, f
}

func requestEnvironmentFiles(h http.Handler, id, query, key string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/v1/agents/environments/"+id+"/files"+query, nil)
	r.Header.Set("Authorization", "Bearer "+key)
	r.Header.Set("OpenAI-Beta", "agents=v1")
	r.Header.Set("X-Tenant-ID", "untrusted")
	w := httptest.NewRecorder()
	h.ServeHTTP(environmentFilesRecorder{w}, r)
	return w
}

type environmentFilesRecorder struct{ *httptest.ResponseRecorder }

func (environmentFilesRecorder) SetWriteDeadline(time.Time) error { return nil }

func environmentFileEntry(name string, size int64) proto.WorkspaceDirectoryEntry {
	return proto.WorkspaceDirectoryEntry{Name: name, Kind: "file", SizeBytes: &size}
}

func decodeEnvironmentFiles(t *testing.T, w *httptest.ResponseRecorder) v1.EnvironmentFileList {
	t.Helper()
	var page v1.EnvironmentFileList
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || page.Data == nil {
		t.Fatal("invalid file page", w.Code, w.Body)
	}
	var fields map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &fields)
	if len(fields) != 2 || fields["data"] == nil || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("unexpected wire page", w.Header(), fields)
	}
	return page
}

func TestEnvironmentFilesOrderingPaginationAndProjection(t *testing.T) {
	for _, order := range []string{"", "asc", "desc"} {
		t.Run("order="+order, func(t *testing.T) {
			h, f := environmentFilesHandler(t, true)
			f.result.Entries = []proto.WorkspaceDirectoryEntry{
				environmentFileEntry("a.txt", 14), environmentFileEntry("z.txt", 5), environmentFileEntry("A.txt", 0), environmentFileEntry("a-b.txt", 6),
				{Name: "directory", Kind: "directory"}, {Name: "symlink", Kind: "symlink"}, {Name: "socket", Kind: "other"},
			}
			q := url.Values{"path": {"/private/workspace/sub/.//"}, "limit": {"2"}}
			if order != "" {
				q.Set("order", order)
			}
			var got []string
			for index := 0; index < 2; index++ {
				page := decodeEnvironmentFiles(t, requestEnvironmentFiles(h, f.environment.ID, "?"+q.Encode(), "files-key"))
				if len(page.Data) != 2 {
					t.Fatal("wrong page size", page)
				}
				for _, item := range page.Data {
					if item.EnvironmentID != f.environment.ID || item.Object != "agent.environment.file" || item.SizeBytes < 0 {
						t.Fatal("wrong projection", item)
					}
					got = append(got, item.Path)
				}
				if index == 0 {
					if page.Next == nil || *page.Next == "" {
						t.Fatal("missing continuation")
					}
					q.Set("page", *page.Next)
				} else if page.Next != nil {
					t.Fatal("unexpected continuation")
				}
			}
			want := []string{"/private/workspace/sub/z.txt", "/private/workspace/sub/a.txt", "/private/workspace/sub/a-b.txt", "/private/workspace/sub/A.txt"}
			if order == "asc" {
				want = []string{want[3], want[2], want[1], want[0]}
			}
			if !reflect.DeepEqual(got, want) || f.directory != "sub" || !reflect.DeepEqual(f.readEnvironment, f.environment) {
				t.Fatal("wrong order or owner", got, f.directory, f.readEnvironment)
			}
		})
	}
}

func TestEnvironmentFilesEmptyRootDefaultsAndSharedAccess(t *testing.T) {
	h, f := environmentFilesHandler(t, true)
	page := decodeEnvironmentFiles(t, requestEnvironmentFiles(h, f.environment.ID, "", "shared-key"))
	if len(page.Data) != 0 || page.Next != nil || f.directory != "" {
		t.Fatal("invalid empty root", page, f.directory)
	}
	for index := 0; index < 21; index++ {
		f.result.Entries = append(f.result.Entries, environmentFileEntry(string(rune('A'+index)), int64(index)))
	}
	page = decodeEnvironmentFiles(t, requestEnvironmentFiles(h, f.environment.ID, "", "files-key"))
	if len(page.Data) != 20 || page.Next == nil || page.Data[0].Path != "/private/workspace/U" {
		t.Fatal("wrong local defaults", page)
	}
	q := url.Values{"page": {*page.Next}}
	page = decodeEnvironmentFiles(t, requestEnvironmentFiles(h, f.environment.ID, "?"+q.Encode(), "files-key"))
	if len(page.Data) != 1 || page.Next != nil || page.Data[0].SizeBytes != 0 {
		t.Fatal("wrong final page", page)
	}
}

func TestEnvironmentFilesAuthorizationPrecedesInspection(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, query := range []string{"", "?path=/foreign-secret/../&page=invalid&limit=999", "?bad=%GG"} {
			h, f := environmentFilesHandler(t, enabled)
			w := requestEnvironmentFiles(h, f.environment.ID, query, "other-key")
			if w.Code != 404 || f.lookups != 1 || f.reads != 0 || strings.Contains(w.Body.String(), "foreign-secret") || strings.Contains(w.Body.String(), f.environment.ID) {
				t.Fatal("foreign resource inspected", w.Code, w.Body, f)
			}
		}
	}
	h, f := environmentFilesHandler(t, true)
	w := requestEnvironmentFiles(h, f.environment.ID, "", "invalid")
	if w.Code != 401 || f.lookups != 0 || f.reads != 0 {
		t.Fatal("unauthenticated read", w.Code, f)
	}
}

func TestEnvironmentFilesRejectsInvalidRequestsBeforeRead(t *testing.T) {
	for _, query := range []string{
		"limit=0", "limit=101", "limit=no", "limit=", "limit=1&limit=2", "order=ASC", "order=", "after=x", "unknown=x", "path=", "path=relative", "path=/private/workspace-sibling", "path=/private/workspace/../workspace", "path=/private/workspace/a/../../workspace", "path=/private/workspace/%00", "path=/private/workspace/%5C", "path=/private/workspace/%0A", "path=/private/workspace/%FF", "path=x&path=y", "path=" + strings.Repeat("a", 4097), "page=", "page=not-json", "page=" + strings.Repeat("a", 1025), "bad=%GG",
	} {
		t.Run(query[:min(len(query), 70)], func(t *testing.T) {
			h, f := environmentFilesHandler(t, true)
			w := requestEnvironmentFiles(h, f.environment.ID, "?"+query, "files-key")
			if w.Code != 400 || f.lookups != 1 || f.reads != 0 {
				t.Fatal("invalid query reached runtime", w.Code, w.Body, f)
			}
		})
	}
}

func TestEnvironmentFilesSafeStoreAndReaderFailures(t *testing.T) {
	for _, target := range []string{"store", "reader"} {
		for _, test := range []struct {
			err    error
			status int
		}{
			{store.ErrNotFound, 404}, {store.ErrInvalidInput, 400}, {execution.ErrExecutionUnavailable, 503}, {errors.New("private-native-secret"), 500},
		} {
			h, f := environmentFilesHandler(t, true)
			if target == "store" {
				f.storeError = test.err
			} else {
				f.readError = test.err
			}
			w := requestEnvironmentFiles(h, f.environment.ID, "", "files-key")
			if w.Code != test.status || strings.Contains(w.Body.String(), "private-native-secret") || strings.Contains(w.Body.String(), `"data"`) || strings.Contains(w.Body.String(), `"next"`) {
				t.Fatal("unsafe error", target, w.Code, w.Body)
			}
		}
	}
	h, f := environmentFilesHandler(t, false)
	if w := requestEnvironmentFiles(h, f.environment.ID, "", "files-key"); w.Code != 503 || f.reads != 0 {
		t.Fatal("missing reader accepted", w.Code, f)
	}
}
