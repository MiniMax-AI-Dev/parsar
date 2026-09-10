package dev

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
)

const uploadTestRun = "00000000-0000-0000-0000-000000009999"
const uploadTestBody = `{"name":"generated-guide","canonical_spec":{"schema_version":1,"kind":"bundle","bundle":{"name":"generated-guide","version":"1.0.0","skills":[{"slug":"guide","instruction":"Answer GUIDE-OK."}]}}}`

type skillUploadTestStore struct {
	RuntimeStore
	run                            store.AgentRunInvocation
	role                           string
	roleErr                        error
	imports                        []store.ImportCapabilityInput
	checkedWorkspace, checkedActor string
}

func (s *skillUploadTestStore) GetAgentRunInvocation(_ context.Context, id string) (store.AgentRunInvocation, error) {
	if id != s.run.RunID {
		return store.AgentRunInvocation{}, store.ErrUnknownAgentRun
	}
	return s.run, nil
}
func (s *skillUploadTestStore) GetWorkspaceMemberRole(_ context.Context, ws, actor string) (string, error) {
	s.checkedWorkspace, s.checkedActor = ws, actor
	return s.role, s.roleErr
}
func (s *skillUploadTestStore) ImportCapability(_ context.Context, in store.ImportCapabilityInput) (store.ImportCapabilityResult, error) {
	s.imports = append(s.imports, in)
	return store.ImportCapabilityResult{}, nil
}

func skillUploadRequest(handler http.Handler, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/agent-authoring/skill-bundles?workspace_id=foreign", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("X-Parsar-Dev-User-ID", "spoofed-owner")
	r = r.WithContext(auth.WithUserID(r.Context(), "spoofed-owner"))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestSkillUploadRequiresCurrentRunRequesterPermission(t *testing.T) {
	signer := auth.NewSkillUploadSigner("test-master-key")
	token, err := signer.Token(uploadTestRun)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, status, actorType, role string
		roleErr                       error
		code                          int
	}{
		{"owner", "running", "user", "owner", nil, 201},
		{"admin", "running", "user", "admin", nil, 201},
		{"member", "running", "user", "member", nil, 403},
		{"viewer", "running", "user", "viewer", nil, 403},
		{"removed", "running", "user", "", store.ErrNotMember, 404},
		{"membership failure", "running", "user", "", errors.New("database unavailable"), 500},
		{"queued", "queued", "user", "owner", nil, 403},
		{"completed", "completed", "user", "owner", nil, 403},
		{"failed", "failed", "user", "owner", nil, 403},
		{"cancelled", "cancelled", "user", "owner", nil, 403},
		{"system", "running", "system", "owner", nil, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &skillUploadTestStore{run: store.AgentRunInvocation{RunID: uploadTestRun, WorkspaceID: "run-workspace", RequestedByID: "real-requester", RequestedByType: tc.actorType, Status: tc.status}, role: tc.role, roleErr: tc.roleErr}
			w := skillUploadRequest(uploadSkillBundle(s, signer), token, uploadTestBody)
			if w.Code != tc.code {
				t.Fatalf("status %d: %s", w.Code, w.Body)
			}
			if tc.code != 201 {
				if len(s.imports) != 0 {
					t.Fatal("denied request wrote a capability")
				}
				return
			}
			if s.checkedActor != "real-requester" || s.checkedWorkspace != "run-workspace" {
				t.Fatal("checked supplied identity instead of run requester")
			}
			if len(s.imports) != 1 || s.imports[0].WorkspaceID != "run-workspace" || s.imports[0].CreatorID != "real-requester" || s.imports[0].Visibility != "workspace" {
				t.Fatalf("wrong import scope: %+v", s.imports)
			}
		})
	}
}

func TestSkillUploadRejectsUnsupportedPayloadsAndCredentials(t *testing.T) {
	signer := auth.NewSkillUploadSigner("test-master-key")
	token, _ := signer.Token(uploadTestRun)
	s := &skillUploadTestStore{run: store.AgentRunInvocation{RunID: uploadTestRun, WorkspaceID: "ws", RequestedByID: "user", RequestedByType: "user", Status: "running"}, role: "owner"}
	handler := uploadSkillBundle(s, signer)
	for _, field := range []string{`"server_entry":"index.js"`, `"client_entry":"index.js"`, `"tools":["tool"]`, `"hooks":["hook"]`, `"credentials":["secret"]`} {
		body := strings.Replace(uploadTestBody, `"skills":`, field+`,"skills":`, 1)
		if w := skillUploadRequest(handler, token, body); w.Code != 400 {
			t.Fatalf("accepted %s: %d %s", field, w.Code, w.Body)
		}
	}
	for _, body := range []string{`{`, strings.Replace(uploadTestBody, `"name":"generated-guide",`, `"visibility":"public","name":"generated-guide",`, 1), strings.Repeat(" ", 1<<20) + uploadTestBody} {
		if w := skillUploadRequest(handler, token, body); w.Code != 400 {
			t.Fatalf("accepted invalid payload: %d", w.Code)
		}
	}
	for _, bad := range []string{"", "runner-credential", token + "x"} {
		if w := skillUploadRequest(handler, bad, uploadTestBody); w.Code != 401 {
			t.Fatalf("accepted invalid bearer: %d", w.Code)
		}
	}
	unknown, _ := signer.Token("00000000-0000-0000-0000-000000008888")
	if w := skillUploadRequest(handler, unknown, uploadTestBody); w.Code != 403 {
		t.Fatal("accepted unknown run")
	}
	if len(s.imports) != 0 {
		t.Fatal("rejected payload wrote data")
	}
}

func TestSkillUploadRealStoreRevokesAndRetainsActor(t *testing.T) {
	db := openDevRouteTestDB(t)
	s := store.New(db)
	ids := store.DefaultDevFixtureIDs()
	_, err := s.SeedDevFixture(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	insertCapabilityExtraUser(t, db, testUserAID, "uploader@example.com")
	insertWorkspaceMember(t, db, testUserAID, "admin")
	insertQueuedRun(t, db, ids, ids.BackendAgentID)
	if _, err := db.Exec(t.Context(), `update agent_runs set status='running', requested_by_id=$1 where id=$2`, testUserAID, uploadTestRun); err != nil {
		t.Fatal(err)
	}
	signer := auth.NewSkillUploadSigner("test-master-key")
	token, _ := signer.Token(uploadTestRun)
	r := chi.NewRouter()
	RegisterSkillUploadRoute(r, s, signer)
	w := skillUploadRequest(r, token, uploadTestBody)
	if w.Code != 201 {
		t.Fatalf("upload %d: %s", w.Code, w.Body)
	}
	var result map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	var actor, ws, visibility string
	if err := db.QueryRow(t.Context(), `select creator_id::text, workspace_id::text, visibility from capability where id=$1`, result["id"]).Scan(&actor, &ws, &visibility); err != nil {
		t.Fatal(err)
	}
	if actor != testUserAID || ws != ids.WorkspaceID || visibility != "workspace" {
		t.Fatalf("persisted scope: %s %s %s", actor, ws, visibility)
	}
	if w := skillUploadRequest(r, token, uploadTestBody); w.Code != 409 {
		t.Fatalf("duplicate: %d %s", w.Code, w.Body)
	}
	newBody := strings.ReplaceAll(uploadTestBody, "generated-guide", "denied-guide")
	if _, err := db.Exec(t.Context(), `update workspace_members set role='member' where workspace_id=$1 and user_id=$2`, ids.WorkspaceID, testUserAID); err != nil {
		t.Fatal(err)
	}
	if w := skillUploadRequest(r, token, newBody); w.Code != 403 {
		t.Fatalf("removed permission: %d %s", w.Code, w.Body)
	}
	if _, err := db.Exec(t.Context(), `update workspace_members set role='admin' where workspace_id=$1 and user_id=$2`, ids.WorkspaceID, testUserAID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(t.Context(), `update agent_runs set status='completed' where id=$1`, uploadTestRun); err != nil {
		t.Fatal(err)
	}
	if w := skillUploadRequest(r, token, newBody); w.Code != 403 {
		t.Fatalf("finished run: %d %s", w.Code, w.Body)
	}
	var count int
	if err := db.QueryRow(t.Context(), `select count(*) from capability where name='denied-guide'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("denied writes: %d %v", count, err)
	}
}
