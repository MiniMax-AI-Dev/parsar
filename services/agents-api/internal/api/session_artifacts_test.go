package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

type artifactFixture struct {
	artifact                                 store.SessionArtifact
	tenant, session, id, environment, cursor string
	limit                                    int
	ascending                                bool
	err                                      error
	calls                                    int
}

func (f *artifactFixture) GetSessionArtifact(_ context.Context, tenant, session, id string) (store.SessionArtifact, error) {
	f.calls++
	f.tenant, f.session, f.id = tenant, session, id
	return f.artifact, f.err
}

func (f *artifactFixture) ListSessionArtifacts(_ context.Context, tenant, session, environment, cursor string, limit int, ascending bool) (store.ArtifactPage, error) {
	f.calls++
	f.tenant, f.session, f.environment, f.cursor, f.limit, f.ascending = tenant, session, environment, cursor, limit, ascending
	return store.ArtifactPage{Artifacts: []store.SessionArtifact{f.artifact}, NextCursor: f.artifact.ID}, f.err
}

func (f *artifactFixture) ReadSessionArtifact(ctx context.Context, tenant, session, id string, consume func(store.SessionArtifact, io.Reader) error) error {
	a, err := f.GetSessionArtifact(ctx, tenant, session, id)
	if err != nil {
		return err
	}
	return consume(a, bytes.NewReader([]byte{0, 255, 1}))
}

func (f *artifactFixture) DeleteSessionArtifact(ctx context.Context, tenant, session, id string) error {
	_, err := f.GetSessionArtifact(ctx, tenant, session, id)
	return err
}

type artifactResponseRecorder struct{ *httptest.ResponseRecorder }

func (*artifactResponseRecorder) SetWriteDeadline(time.Time) error { return nil }

func TestSessionArtifactRoutesAndPublicProjection(t *testing.T) {
	f := &artifactFixture{artifact: store.SessionArtifact{ID: "artifact", SessionID: "session", EnvironmentID: "environment", TurnID: "turn", Path: "/workspace/outputs/a.bin", SizeBytes: 3, CreatedAt: time.Unix(123, 456)}}
	h, _, tenant := testHandler(t, WithSessionArtifacts(f))
	request := func(method, suffix, beta string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/v1/agents/sessions/session/artifacts"+suffix, nil)
		r.Header.Set("Authorization", "Bearer test-api-key")
		r.Header.Set("OpenAI-Beta", beta)
		r.Header.Set("X-Tenant-ID", "untrusted")
		w := &artifactResponseRecorder{httptest.NewRecorder()}
		h.ServeHTTP(w, r)
		return w.ResponseRecorder
	}
	w := request("GET", "/artifact", "agents=v1")
	var got v1.SessionArtifact
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Object != "agent.session.artifact" || got.CreatedAt != 123 || got.SizeBytes != 3 || got.Path != f.artifact.Path || got.TurnID != "turn" || got.EnvironmentID != "environment" || got.SessionID != "session" || got.ID != "artifact" {
		t.Fatalf("metadata projection: %d %s", w.Code, w.Body)
	}
	if f.tenant != tenant || f.session != "session" || f.id != "artifact" {
		t.Fatalf("untrusted request scope: %+v", f)
	}
	w = request("GET", "?environment_id=environment&after=previous&limit=1&order=asc", "agents=v1")
	var page v1.SessionArtifactList
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || !page.HasMore || len(page.Data) != 1 || f.environment != "environment" || f.cursor != "previous" || f.limit != 1 || !f.ascending {
		t.Fatalf("filtered page: %d %s %+v", w.Code, w.Body, f)
	}
	w = request("GET", "/artifact/content", "agents=v1")
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), []byte{0, 255, 1}) || w.Header().Get("Content-Type") != "application/octet-stream" || w.Header().Get("Content-Length") != "3" {
		t.Fatalf("content: %d %s %v", w.Code, w.Body, w.Header())
	}
	w = request("DELETE", "/artifact", "agents=v1")
	var deleted v1.SessionArtifactDeleted
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &deleted) != nil || !deleted.Deleted || deleted.ID != "artifact" || deleted.Object != "agent.session.artifact.deleted" {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	for _, suffix := range []string{"?limit=0", "?limit=101", "?order=random", "?environment_id=a&environment_id=b", "?limit=1&limit=2", "?unknown=x", "/artifact?unknown=x", "/artifact/content?unknown=x"} {
		before := f.calls
		if w := request("GET", suffix, "agents=v1"); w.Code != 400 || f.calls != before {
			t.Fatalf("invalid query reached storage: %s %d", suffix, w.Code)
		}
	}
	for _, route := range []struct{ method, suffix string }{{"GET", ""}, {"GET", "/artifact"}, {"GET", "/artifact/content"}, {"DELETE", "/artifact"}} {
		before := f.calls
		if w := request(route.method, route.suffix, ""); w.Code != 400 || f.calls != before {
			t.Fatalf("missing Beta accepted: %s %s %d", route.method, route.suffix, w.Code)
		}
		f.err = store.ErrNotFound
		if w := request(route.method, route.suffix, "agents=v1"); w.Code != 404 {
			t.Fatalf("not-found mapping: %s %s %d", route.method, route.suffix, w.Code)
		}
	}
	if w := request(http.MethodPost, "", "agents=v1"); w.Code != 405 {
		t.Fatalf("invented create route: %d", w.Code)
	}
}
