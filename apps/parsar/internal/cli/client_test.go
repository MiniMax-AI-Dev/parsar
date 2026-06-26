package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// stubServer records the most recent request and serves the canned
// response. Tests inspect rec after the call to assert wire-level
// correctness.
type recordedReq struct {
	method string
	path   string
	query  string
	body   string
	auth   string
	accept string
}

func stubServer(t *testing.T, status int, body string) (*httptest.Server, *recordedReq) {
	t.Helper()
	rec := &recordedReq{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		rec.method = r.Method
		rec.path = r.URL.Path
		rec.query = r.URL.RawQuery
		rec.body = string(raw)
		rec.auth = r.Header.Get("Authorization")
		rec.accept = r.Header.Get("Accept")
		if body != "" {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(status)
		if body != "" {
			_, _ = w.Write([]byte(body))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

func newTestClient(srv *httptest.Server) *client {
	return newClient(Config{
		ServerURL:   srv.URL,
		RunnerToken: "tok-abc",
	})
}

func TestClientListFragments(t *testing.T) {
	body := `{"fragments":[{"id":"f1","workspace_id":"ws-1","title":"T","body":"B","tags":["a"],"source":"agent","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}]}`
	srv, rec := stubServer(t, http.StatusOK, body)
	rows, err := newTestClient(srv).ListFragments(context.Background(), []string{"a", "b"}, "agent", 25)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != "f1" || rows[0].Source != "agent" {
		t.Fatalf("rows = %+v", rows)
	}
	if rec.method != http.MethodGet {
		t.Errorf("method = %q, want GET", rec.method)
	}
	if rec.path != "/api/v1/agent-runtime/spec/fragments" {
		t.Errorf("path = %q", rec.path)
	}
	if !strings.Contains(rec.query, "tag=a%2Cb") || !strings.Contains(rec.query, "source=agent") || !strings.Contains(rec.query, "limit=25") {
		t.Errorf("query = %q", rec.query)
	}
	if rec.auth != "Bearer tok-abc" {
		t.Errorf("auth = %q", rec.auth)
	}
}

func TestClientCreateFragment(t *testing.T) {
	body := `{"id":"f-new","workspace_id":"ws-1","title":"T","body":"B","tags":["a"],"source":"agent","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`
	srv, rec := stubServer(t, http.StatusCreated, body)
	out, err := newTestClient(srv).CreateFragment(context.Background(), "T", "B", []string{"a"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if out.ID != "f-new" {
		t.Errorf("id = %q", out.ID)
	}
	if rec.method != http.MethodPost {
		t.Errorf("method = %q, want POST", rec.method)
	}
	var sent createFragmentRequest
	if err := json.Unmarshal([]byte(rec.body), &sent); err != nil {
		t.Fatalf("decode sent body: %v (body=%s)", err, rec.body)
	}
	if sent.Title != "T" || sent.Body != "B" || len(sent.Tags) != 1 || sent.Tags[0] != "a" {
		t.Errorf("body = %+v", sent)
	}
}

func TestClientUpdateFragmentPathEscape(t *testing.T) {
	srv, rec := stubServer(t, http.StatusOK, `{"id":"f 1","workspace_id":"ws-1","title":"","body":"","tags":[],"source":"agent","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`)
	_, err := newTestClient(srv).UpdateFragment(context.Background(), "f 1", "", "", nil)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if rec.path != "/api/v1/agent-runtime/spec/fragments/f 1" {
		t.Errorf("path = %q (the server already URL-decoded; we just need it to round-trip)", rec.path)
	}
}

func TestClientDeleteFragmentNoContent(t *testing.T) {
	srv, rec := stubServer(t, http.StatusNoContent, "")
	if err := newTestClient(srv).DeleteFragment(context.Background(), "f1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if rec.method != http.MethodDelete {
		t.Errorf("method = %q", rec.method)
	}
	if !strings.HasSuffix(rec.path, "/spec/fragments/f1") {
		t.Errorf("path = %q", rec.path)
	}
}

func TestClientListMemoriesScopeRequired(t *testing.T) {
	body := `{"memories":[{"id":"m1","scope":"user","user_id":"u","memory_type":"user","body":"hi","tags":[],"source":"agent","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}]}`
	srv, rec := stubServer(t, http.StatusOK, body)
	rows, err := newTestClient(srv).ListMemories(context.Background(), "user", "feedback", []string{"x"}, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows: %+v", rows)
	}
	if !strings.Contains(rec.query, "scope=user") {
		t.Errorf("missing scope: query=%q", rec.query)
	}
	if !strings.Contains(rec.query, "memory_type=feedback") {
		t.Errorf("missing memory_type: query=%q", rec.query)
	}
}

func TestClientCreateMemoryBody(t *testing.T) {
	srv, rec := stubServer(t, http.StatusCreated, `{"id":"m1","scope":"project","user_id":"u","project_id":"p","memory_type":"feedback","body":"b","why":"w","tags":["x"],"source":"agent","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`)
	_, err := newTestClient(srv).CreateMemory(context.Background(), "project", "feedback", "ttl", "body", "why", []string{"x"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var sent createMemoryRequest
	if err := json.Unmarshal([]byte(rec.body), &sent); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if sent.Scope != "project" || sent.MemoryType != "feedback" || sent.Why != "why" {
		t.Errorf("sent = %+v", sent)
	}
}

func TestClientSnapshot(t *testing.T) {
	body := `{"spec_block":"<spec>x</spec>","memory_block":"<memory></memory>","memory_write_guide":"<guide></guide>","incremental_memory":""}`
	srv, rec := stubServer(t, http.StatusOK, body)
	snap, err := newTestClient(srv).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snap.SpecBlock != "<spec>x</spec>" {
		t.Errorf("spec = %q", snap.SpecBlock)
	}
	if rec.path != "/api/v1/agent-runtime/injection/snapshot" {
		t.Errorf("path = %q", rec.path)
	}
}

func TestClientIncrementalQuery(t *testing.T) {
	body := `{"spec_block":"","memory_block":"","memory_write_guide":"","incremental_memory":"X"}`
	srv, rec := stubServer(t, http.StatusOK, body)
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if _, err := newTestClient(srv).Incremental(context.Background(), when); err != nil {
		t.Fatalf("incremental: %v", err)
	}
	if !strings.Contains(rec.query, "since=2026-01-02T03%3A04%3A05Z") {
		t.Errorf("missing since: query=%q", rec.query)
	}
}

func TestClientErrorJSON(t *testing.T) {
	srv, _ := stubServer(t, http.StatusBadRequest, `{"error":"bad_json","message":"oops"}`)
	_, err := newTestClient(srv).Snapshot(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	var ae *apiError
	if !errors.As(err, &ae) {
		t.Fatalf("expected apiError, got %T (%v)", err, err)
	}
	if ae.Status != http.StatusBadRequest || ae.Code != "bad_json" || ae.Message != "oops" {
		t.Errorf("apiError = %+v", ae)
	}
}

func TestClientErrorPlainText(t *testing.T) {
	srv, _ := stubServer(t, http.StatusBadGateway, "upstream down")
	_, err := newTestClient(srv).Snapshot(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	var ae *apiError
	if !errors.As(err, &ae) {
		t.Fatalf("expected apiError, got %T", err)
	}
	if ae.Status != http.StatusBadGateway {
		t.Errorf("status = %d", ae.Status)
	}
	if ae.Message != "upstream down" {
		t.Errorf("message = %q", ae.Message)
	}
}

func TestIsNotFound(t *testing.T) {
	srv, _ := stubServer(t, http.StatusNotFound, `{"error":"not_found","message":""}`)
	err := newTestClient(srv).DeleteFragment(context.Background(), "missing")
	if !IsNotFound(err) {
		t.Fatalf("IsNotFound = false on 404 (%v)", err)
	}
	if IsNotFound(errors.New("other")) {
		t.Fatal("IsNotFound should be false on plain errors")
	}
}
