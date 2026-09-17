package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

type environmentFileCreateFixture struct {
	*environmentFilesFixture
	writes    int
	path      string
	data      []byte
	err       error
	wrongSize bool
}

func (f *environmentFileCreateFixture) WriteEnvironmentFile(_ context.Context, environment store.Environment, path string, data []byte) (int64, error) {
	f.writes++
	f.readEnvironment, f.path, f.data = environment, path, append([]byte(nil), data...)
	if f.wrongSize {
		return int64(len(data) + 1), nil
	}
	return int64(len(data)), f.err
}

func environmentFileCreateHandler(t *testing.T, extra ...Option) (http.Handler, *environmentFileCreateFixture) {
	t.Helper()
	_, base := environmentFilesHandler(t, false)
	base.environment.Configuration = json.RawMessage(`{"type":"openai_hosted","network":{"access":"disabled"}}`)
	f := &environmentFileCreateFixture{environmentFilesFixture: base}
	auth, err := NewAuthenticator([]APIKey{
		{OrganizationID: "org", ProjectID: "project", SubjectKind: "user", SubjectID: "caller", TokenSHA256: device.HashCredential("files-key"), TenantID: f.environment.TenantID},
		{OrganizationID: "org", ProjectID: "other", SubjectKind: "user", SubjectID: "other", TokenSHA256: device.HashCredential("other-key"), TenantID: uuid.NewString()},
	})
	if err != nil {
		t.Fatal(err)
	}
	options := append([]Option{WithEnvironmentFileWriter(f), WithEnvironmentDirectoryReader(f)}, extra...)
	h, err := NewHandler(f, auth, "codex", options...)
	if err != nil {
		t.Fatal(err)
	}
	return h, f
}

func requestCreateEnvironmentFile(h http.Handler, id, body, key string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/v1/agents/environments/"+id+"/files", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+key)
	r.Header.Set("OpenAI-Beta", "agents=v1")
	w := httptest.NewRecorder()
	h.ServeHTTP(environmentFilesRecorder{w}, r)
	return w
}

func TestEnvironmentFileCreateInlineAndLocalListing(t *testing.T) {
	for _, data := range [][]byte{{}, {0, 1, 255}, bytes.Repeat([]byte{0, 255, 7}, 400000)} {
		h, f := environmentFileCreateHandler(t)
		body, _ := json.Marshal(map[string]any{"type": "inline", "data": base64.StdEncoding.EncodeToString(data), "path": "/workspace/input.bin"})
		w := requestCreateEnvironmentFile(h, f.environment.ID, string(body), "files-key")
		var file v1.EnvironmentFile
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &file) != nil {
			t.Fatalf("create: %d %s", w.Code, w.Body)
		}
		if f.writes != 1 || f.path != "input.bin" || !bytes.Equal(f.data, data) || f.readEnvironment.ID != f.environment.ID || file.EnvironmentID != f.environment.ID || file.Path != "/workspace/input.bin" || file.Object != "agent.environment.file" || file.SizeBytes != int64(len(data)) {
			t.Fatal("scope/data/metadata changed")
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(w.Body.Bytes(), &fields) != nil || len(fields) != 4 {
			t.Fatal("nonprotocol response fields")
		}
		f.result.Entries = append(f.result.Entries, environmentFileEntry("input.bin", int64(len(data))))
		page := decodeEnvironmentFiles(t, requestEnvironmentFiles(h, f.environment.ID, "?order=asc", "files-key"))
		if len(page.Data) != 1 || page.Data[0] != file || f.directory != "" {
			t.Fatal("local list/create path mismatch", page)
		}
	}
}

func TestEnvironmentFileCreateRejectsInvalidUnionAndPath(t *testing.T) {
	for _, body := range []string{
		`null`, `[]`, `{}`, `{"type":"inline","path":"/workspace/a"}`,
		`{"type":"inline","data":null,"path":"/workspace/a"}`,
		`{"type":"inline","data":"","path":null}`,
		`{"type":"inline","data":"?","path":"/workspace/a"}`,
		`{"type":"inline","data":"","path":"/workspace"}`,
		`{"type":"inline","data":"","path":"/workspacex/a"}`,
		`{"type":"inline","data":"","path":"/workspace/../secret"}`,
		`{"type":"inline","data":"","path":"/workspace/a/"}`,
		`{"type":"inline","data":"","path":"/workspace/a","file_id":"x"}`,
		`{"type":"file_id","file_id":null,"path":"/workspace/a"}`,
		`{"type":"inline","data":"","path":"/workspace/a"} {}`,
	} {
		h, f := environmentFileCreateHandler(t)
		w := requestCreateEnvironmentFile(h, f.environment.ID, body, "files-key")
		if w.Code != 400 || f.writes != 0 {
			t.Fatalf("invalid body admitted %s: %d", body, w.Code)
		}
	}
}

func TestEnvironmentFileCreateAuthorityAndUncertainResults(t *testing.T) {
	body := `{"type":"inline","data":"YWJj","path":"/workspace/a"}`
	h, f := environmentFileCreateHandler(t)
	for _, key := range []string{"other-key", "invalid"} {
		w := requestCreateEnvironmentFile(h, f.environment.ID, body, key)
		if (key == "other-key" && w.Code != 404) || (key == "invalid" && w.Code != 401) || f.writes != 0 {
			t.Fatal("caller authority bypassed", w.Code)
		}
	}
	f.err = execution.ErrExecutionUnavailable
	if w := requestCreateEnvironmentFile(h, f.environment.ID, body, "files-key"); w.Code != 503 {
		t.Fatal("uncertain write reported success", w.Code)
	}
	f.err, f.wrongSize = nil, true
	if w := requestCreateEnvironmentFile(h, f.environment.ID, body, "files-key"); w.Code != 503 {
		t.Fatal("wrong byte count reported success", w.Code)
	}
	f.environment.Configuration = json.RawMessage(`{"type":"self_hosted","workspace_directory":"/workspace"}`)
	f.writes = 0
	if w := requestCreateEnvironmentFile(h, f.environment.ID, body, "files-key"); w.Code != 503 || f.writes != 0 {
		t.Fatal("unsupported placement admitted", w.Code)
	}
}
