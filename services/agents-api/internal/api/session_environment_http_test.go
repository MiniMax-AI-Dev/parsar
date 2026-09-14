package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

type environmentHTTPFixture struct{ streamFixture }

func (f *environmentHTTPFixture) ListSessions(_ context.Context, tenant, _ string, _ int, _ bool, _ *string) (store.SessionPage, error) {
	if tenant != f.session.TenantID {
		return store.SessionPage{}, store.ErrNotFound
	}
	return store.SessionPage{Sessions: []store.Session{f.session}}, nil
}

func (f *environmentHTTPFixture) UpdateSessionMetadata(ctx context.Context, tenant, session string, metadata map[string]string) (store.Session, error) {
	value, err := f.GetSession(ctx, tenant, session)
	value.Metadata = metadata
	return value, err
}

func TestSelfHostedSessionHTTPReadListMetadataAndLiveStream(t *testing.T) {
	session := environmentSession()
	session.TenantID = uuid.NewString()
	session.Environment.TenantID = session.TenantID
	session.EnvironmentInputActivity = &store.EnvironmentInputActivity{
		Status: "requires_action", EnvironmentID: session.Environment.ID, LastActiveAt: time.Unix(1700000100, 0),
	}
	fixture := &environmentHTTPFixture{streamFixture{session: session, changes: []store.SessionChange{{
		Sequence: 11, Event: v1.SessionEvent{Type: "agent.session.requires_action", EventID: "activity", SessionID: session.ID},
		EnvironmentInputActivity: session.EnvironmentInputActivity,
	}}}}
	auth, err := NewAuthenticator([]APIKey{{
		OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "test-runner",
		TokenSHA256: device.HashCredential("key"), TenantID: session.TenantID,
	}})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(fixture, auth, "codex", WithEnvironmentRemoteURL(environmentOrigin))
	if err != nil {
		t.Fatal(err)
	}
	want, err := sessionResponse(session, environmentOrigin)
	if err != nil {
		t.Fatal(err)
	}
	check := func(value v1.Session) {
		t.Helper()
		value.Metadata = want.Metadata
		actual, _ := json.Marshal(value)
		expected, _ := json.Marshal(want)
		if string(actual) != string(expected) {
			t.Fatal("Session projections differ", string(actual), string(expected))
		}
	}
	for _, request := range []struct{ method, path, body string }{
		{http.MethodGet, "/v1/agents/sessions/session", ""},
		{http.MethodGet, "/v1/agents/sessions", ""},
		{http.MethodPost, "/v1/agents/sessions/session", `{"metadata":{"label":"updated"}}`},
	} {
		r := httptest.NewRequest(request.method, request.path, strings.NewReader(request.body))
		r.Host = "untrusted-host.example"
		r.Header.Set("Authorization", "Bearer key")
		r.Header.Set("OpenAI-Beta", "agents=v1")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatal(request.path, w.Code, w.Body.String())
		}
		var value v1.Session
		if request.path == "/v1/agents/sessions" {
			var list v1.SessionList
			if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list.Data) != 1 {
				t.Fatal(w.Body.String(), err)
			}
			value = list.Data[0]
		} else if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		check(value)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	r, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/v1/agents/sessions/session/events", nil)
	r.Host = "untrusted-host.example"
	r.Header.Set("Authorization", "Bearer key")
	r.Header.Set("OpenAI-Beta", "agents=v1")
	response, err := server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatal("self-hosted live stream unavailable", response.StatusCode)
	}
	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		if data, ok := strings.CutPrefix(scanner.Text(), "data: "); ok {
			var event v1.SessionEvent
			if err := json.Unmarshal([]byte(data), &event); err != nil || event.Session == nil {
				t.Fatal(data, err)
			}
			check(*event.Session)
			return
		}
	}
	t.Fatal("stream ended before pending activity", scanner.Err())
}
