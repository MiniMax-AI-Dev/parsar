package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

type sourceFilesFixture struct {
	mu     sync.Mutex
	tenant string
	file   store.SourceFile
	data   []byte
	reads  int
}

func (f *sourceFilesFixture) CreateSourceFile(_ context.Context, tenant string, upload func(io.Writer) (store.SourceFileUpload, error)) (store.SourceFile, error) {
	var body bytes.Buffer
	input, err := upload(&body)
	if err != nil {
		return store.SourceFile{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tenant, f.data = tenant, body.Bytes()
	f.file = store.SourceFile{ID: "file-" + uuid.NewString(), Filename: input.Filename, Purpose: input.Purpose, SizeBytes: int64(body.Len()), CreatedAt: time.Unix(123, 0)}
	return f.file, nil
}

func (f *sourceFilesFixture) GetSourceFile(_ context.Context, tenant, id string) (store.SourceFile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.file.ID != id || f.tenant != tenant {
		return store.SourceFile{}, store.ErrNotFound
	}
	return f.file, nil
}

func (f *sourceFilesFixture) ReadSourceFile(ctx context.Context, tenant, id string, consume func(store.SourceFile, io.Reader) error) error {
	file, err := f.GetSourceFile(ctx, tenant, id)
	if err != nil {
		return err
	}
	f.mu.Lock()
	f.reads++
	data := bytes.Clone(f.data)
	f.mu.Unlock()
	return consume(file, bytes.NewReader(data))
}

func (f *sourceFilesFixture) DeleteSourceFile(ctx context.Context, tenant, id string) error {
	if _, err := f.GetSourceFile(ctx, tenant, id); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.file = store.SourceFile{}
	f.data = nil
	return nil
}

func sourceMultipart(t *testing.T, fields []string, data []byte) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for _, field := range fields {
		if field == "file" {
			part, err := w.CreateFormFile("file", "source.bin")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := part.Write(data); err != nil {
				t.Fatal(err)
			}
		} else {
			name, value, _ := strings.Cut(field, "=")
			if err := w.WriteField(name, value); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes(), w.FormDataContentType()
}

func sourceRequest(t *testing.T, server *httptest.Server, method, path, key, contentType string, body []byte) (int, []byte) {
	t.Helper()
	r, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+key)
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	resp, err := server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, data
}

func TestSourceFilesPublicLifecycleAndEnvironmentCopy(t *testing.T) {
	for _, fields := range [][]string{{"file", "purpose=user_data"}, {"purpose=user_data", "file"}} {
		f := &sourceFilesFixture{}
		h, env := environmentFileCreateHandler(t, WithSourceFiles(f))
		server := httptest.NewServer(h)
		t.Cleanup(server.Close)
		data := []byte{0, 1, 255, 7}
		body, contentType := sourceMultipart(t, fields, data)
		status, raw := sourceRequest(t, server, "POST", "/v1/files", "files-key", contentType, body)
		var file v1.SourceFile
		if status != 200 || json.Unmarshal(raw, &file) != nil || file.Bytes != int64(len(data)) || file.Object != "file" || file.Purpose != "user_data" || file.Status != "processed" || file.ExpiresAt != nil {
			t.Fatalf("upload: %d %s", status, raw)
		}
		for _, path := range []string{"/v1/files/" + file.ID, "/v1/files/" + file.ID + "/content"} {
			if status, _ := sourceRequest(t, server, "GET", path, "other-key", "", nil); status != 404 {
				t.Fatalf("foreign source: %d", status)
			}
		}
		if status, got := sourceRequest(t, server, "GET", "/v1/files/"+file.ID+"/content", "files-key", "", nil); status != 200 || !bytes.Equal(got, data) {
			t.Fatalf("content: %d %v", status, got)
		}
		copyBody := `{"type":"file_id","file_id":"` + file.ID + `","path":"/workspace/source.bin"}`
		if status, _ := sourceRequest(t, server, "POST", "/v1/agents/environments/"+env.environment.ID+"/files", "files-key", "application/json", []byte(copyBody)); status != 400 {
			t.Fatal("Agents Beta requirement changed", status)
		}
		if got := requestCreateEnvironmentFile(h, env.environment.ID, copyBody, "files-key"); got.Code != 200 || !bytes.Equal(env.data, data) || env.writes != 1 {
			t.Fatalf("copy: %d %s", got.Code, got.Body)
		}
		if status, _ := sourceRequest(t, server, "DELETE", "/v1/files/"+file.ID, "other-key", "", nil); status != 404 {
			t.Fatal("foreign deletion allowed")
		}
		status, raw = sourceRequest(t, server, "DELETE", "/v1/files/"+file.ID, "files-key", "", nil)
		var deleted v1.SourceFileDeleted
		if status != 200 || json.Unmarshal(raw, &deleted) != nil || !deleted.Deleted || deleted.ID != file.ID || deleted.Object != "file" {
			t.Fatalf("delete: %d %s", status, raw)
		}
		if got := requestCreateEnvironmentFile(h, env.environment.ID, copyBody, "files-key"); got.Code != 404 || env.writes != 1 || !bytes.Equal(env.data, data) {
			t.Fatal("deleted source reused or prior copy changed", got.Code)
		}
	}
}

func TestSourceFilesRejectIncompleteOrUnsupportedMultipart(t *testing.T) {
	for _, fields := range [][]string{{"file"}, {"purpose=user_data"}, {"file", "file", "purpose=user_data"}, {"file", "purpose=user_data", "purpose=user_data"}, {"file", "purpose=batch"}, {"file", "purpose=user_data", "expires_after[seconds]=3600"}, {"file", "purpose=user_data", "extra=x"}} {
		f := &sourceFilesFixture{}
		h, _ := environmentFileCreateHandler(t, WithSourceFiles(f))
		server := httptest.NewServer(h)
		body, contentType := sourceMultipart(t, fields, []byte("discard"))
		status, _ := sourceRequest(t, server, "POST", "/v1/files", "files-key", contentType, body)
		server.Close()
		if status != 400 || f.file.ID != "" {
			t.Fatalf("invalid multipart accepted %v: %d", fields, status)
		}
	}
	f := &sourceFilesFixture{}
	h, _ := environmentFileCreateHandler(t, WithSourceFiles(f))
	server := httptest.NewServer(h)
	defer server.Close()
	body, contentType := sourceMultipart(t, []string{"purpose=user_data", "file"}, []byte("truncated"))
	if status, _ := sourceRequest(t, server, "POST", "/v1/files", "files-key", contentType, body[:len(body)-20]); status != 400 || f.file.ID != "" {
		t.Fatal("truncated body committed", status)
	}
	if status, _ := sourceRequest(t, server, "POST", "/v1/files", "invalid", contentType, body); status != 401 || f.file.ID != "" {
		t.Fatal("unauthenticated upload admitted", status)
	}
}

func TestEnvironmentSourceCopyEnforcesScopeUnionAndSize(t *testing.T) {
	f := &sourceFilesFixture{file: store.SourceFile{ID: "file-" + uuid.NewString(), SizeBytes: proto.WorkspaceWriteMaxBytes + 1}}
	h, env := environmentFileCreateHandler(t, WithSourceFiles(f))
	f.tenant = env.environment.TenantID
	body := `{"type":"file_id","file_id":"` + f.file.ID + `","path":"/workspace/source.bin"}`
	if got := requestCreateEnvironmentFile(h, env.environment.ID, body, "other-key"); got.Code != 404 || f.reads != 0 {
		t.Fatal("source read before Environment authority")
	}
	if got := requestCreateEnvironmentFile(h, env.environment.ID, body, "files-key"); got.Code != 413 || env.writes != 0 {
		t.Fatal("oversized source dispatched", got.Code)
	}
	for _, extra := range []string{`,"data":null`, `,"data":""`} {
		invalid := strings.TrimSuffix(body, "}") + extra + "}"
		if got := requestCreateEnvironmentFile(h, env.environment.ID, invalid, "files-key"); got.Code != 400 || env.writes != 0 {
			t.Fatal("mixed union admitted", got.Code)
		}
	}
}

func TestSourceContentDoesNotCompleteTruncatedResponse(t *testing.T) {
	f := &sourceFilesFixture{file: store.SourceFile{ID: "file-" + uuid.NewString(), Filename: "source.bin", SizeBytes: 3}, data: []byte("x")}
	h, env := environmentFileCreateHandler(t, WithSourceFiles(f))
	f.tenant = env.environment.TenantID
	server := httptest.NewServer(h)
	defer server.Close()
	r, err := http.NewRequestWithContext(t.Context(), "GET", server.URL+"/v1/files/"+f.file.ID+"/content", nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer files-key")
	resp, err := server.Client().Do(r)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if _, err := io.ReadAll(resp.Body); err == nil {
		t.Fatal("truncated content completed as a successful response")
	}
}
