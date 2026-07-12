package runtime_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	runtimeapi "github.com/MiniMax-AI-Dev/parsar/server/internal/api/runtime"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// End-to-end: chi router + runtime routes + simulated admin session.
func TestRuntimeHTTPSurfaceDaemonPairingRoundTrip(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	ids := store.DefaultDevFixtureIDs()
	if _, err := store.New(db).InsertDevFixture(ctx, ids); err != nil {
		t.Fatalf("seed dev fixture: %v", err)
	}
	s := store.New(db)

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			req = req.WithContext(auth.WithUserID(req.Context(), ids.UserID))
			next.ServeHTTP(w, req)
		})
	})
	runtimeapi.RegisterRoutes(r, runtimeapi.Deps{Store: s})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	pairRes := postJSON(t, srv.URL+"/api/v1/workspaces/"+ids.WorkspaceID+"/runtimes",
		map[string]any{
			"name": "alice-laptop",
		}, http.StatusCreated)
	runtimeOut := pairRes["runtime"].(map[string]any)
	rtID := runtimeOut["id"].(string)
	if runtimeOut["type"] != "agent_daemon" || runtimeOut["provider"] != "agent_daemon" {
		t.Fatalf("pairing runtime type/provider = %v/%v, want agent_daemon/agent_daemon", runtimeOut["type"], runtimeOut["provider"])
	}
	if runtimeOut["liveness"] != "pending_pairing" {
		t.Fatalf("new runtime liveness = %v, want pending_pairing", runtimeOut["liveness"])
	}
	plaintextToken := pairRes["pairing_token"].(string)
	if !strings.HasPrefix(plaintextToken, "rtk_") {
		t.Fatalf("pairing token prefix wrong: %q", plaintextToken)
	}

	postJSON(t, srv.URL+"/api/v1/workspaces/"+ids.WorkspaceID+"/runtimes",
		map[string]any{
			"name": "legacy-local",
			"type": "local",
		}, http.StatusBadRequest)

	pairOut := postJSON(t, srv.URL+"/api/v1/runtimes/pair",
		map[string]any{
			"pairing_token":     plaintextToken,
			"hostname":          "alice.local",
			"version":           "0.1.0",
			"runner_public_key": "fakepubkey==",
		}, http.StatusOK)
	credential := pairOut["runner_credential"].(string)
	if !strings.HasPrefix(credential, "rtc_") {
		t.Fatalf("runner credential prefix wrong: %q", credential)
	}
	pairedRuntime := pairOut["runtime"].(map[string]any)
	if pairedRuntime["liveness"] != "offline" {
		t.Fatalf("after pair liveness = %v, want offline", pairedRuntime["liveness"])
	}
	if cfg, ok := pairedRuntime["config"].(map[string]any); ok {
		if _, leaked := cfg["runner_credential_hash"]; leaked {
			t.Fatalf("runtime DTO leaked runner_credential_hash: %#v", cfg)
		}
	}

	hb := postJSONBearer(t, srv.URL+"/api/v1/runtimes/"+rtID+"/heartbeat",
		credential, nil, http.StatusOK)
	if hb["liveness"] != "online" {
		t.Fatalf("heartbeat liveness = %v, want online", hb["liveness"])
	}

	postJSONBearer(t, srv.URL+"/api/v1/runtimes/"+rtID+"/heartbeat",
		"rtc_wrongtokenwrongtokenwrongtoken00000000000000000000000000000000",
		nil, http.StatusUnauthorized)
	respondsWith(t, srv.URL+"/api/v1/runtimes/"+rtID+"/runs/claim",
		credential, http.StatusNotFound)

	respondsWithGet(t, srv.URL+"/api/v1/workspaces/"+ids.WorkspaceID+"/runtimes?type=bogus",
		http.StatusBadRequest)
	respondsWithGet(t, srv.URL+"/api/v1/workspaces/"+ids.WorkspaceID+"/runtimes?type=agent_daemon",
		http.StatusOK)
}

func TestSharedRuntimeStaticTokenPair(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	s := store.New(db)

	const sharedToken = "test-shared-runtime-token"
	r := chi.NewRouter()
	runtimeapi.RegisterRunnerRoutes(r, runtimeapi.Deps{Store: s, SharedRuntimeToken: sharedToken})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	pairBody := map[string]any{
		"pairing_token":     sharedToken,
		"hostname":          "parsar-runtime",
		"version":           "0.1.0",
		"runner_public_key": "fakepubkey==",
	}
	postJSON(t, srv.URL+"/api/v1/runtimes/pair", pairBody, http.StatusServiceUnavailable)

	ids := store.DefaultDevFixtureIDs()
	if _, err := s.InsertDevFixture(ctx, ids); err != nil {
		t.Fatalf("seed dev fixture: %v", err)
	}

	pairOut := postJSON(t, srv.URL+"/api/v1/runtimes/pair", pairBody, http.StatusOK)
	credential := pairOut["runner_credential"].(string)
	if !strings.HasPrefix(credential, "rtc_") {
		t.Fatalf("runner credential prefix wrong: %q", credential)
	}
	rt := pairOut["runtime"].(map[string]any)
	if rt["name"] != store.SharedRuntimeName {
		t.Fatalf("runtime name = %v, want %q", rt["name"], store.SharedRuntimeName)
	}
	if rt["workspace_id"] != ids.WorkspaceID {
		t.Fatalf("workspace_id = %v, want %v", rt["workspace_id"], ids.WorkspaceID)
	}
	rtID := rt["id"].(string)

	repairOut := postJSON(t, srv.URL+"/api/v1/runtimes/pair", pairBody, http.StatusOK)
	rt2 := repairOut["runtime"].(map[string]any)
	if rt2["id"] != rtID {
		t.Fatalf("re-pair changed runtime id: %v -> %v", rtID, rt2["id"])
	}
	if repairOut["runner_credential"] == credential {
		t.Fatal("re-pair returned the same runner credential")
	}

	hb := postJSONBearer(t, srv.URL+"/api/v1/runtimes/"+rtID+"/heartbeat",
		repairOut["runner_credential"].(string), nil, http.StatusOK)
	if hb["liveness"] != "online" {
		t.Fatalf("heartbeat liveness = %v, want online", hb["liveness"])
	}

	postJSON(t, srv.URL+"/api/v1/runtimes/pair", map[string]any{
		"pairing_token":     "test-shared-runtime-tokeX",
		"hostname":          "parsar-runtime",
		"version":           "0.1.0",
		"runner_public_key": "fakepubkey==",
	}, http.StatusUnauthorized)
}

func TestRuntimeListFiltersSelectableLocalDaemon(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	ids := store.DefaultDevFixtureIDs()
	if _, err := store.New(db).InsertDevFixture(ctx, ids); err != nil {
		t.Fatalf("seed dev fixture: %v", err)
	}
	s := store.New(db)

	localCreate, err := s.CreateRuntimePairing(ctx, store.CreateRuntimePairingInput{
		WorkspaceID: ids.WorkspaceID,
		Type:        store.RuntimeTypeAgentDaemon,
		Name:        "local-selectable",
		Provider:    store.RuntimeProviderAgentDaemon,
		OwnerUserID: ids.UserID,
		ActorID:     ids.UserID,
	})
	if err != nil {
		t.Fatalf("create local runtime: %v", err)
	}
	localRuntime, err := s.ConsumePairingToken(ctx, store.ConsumePairingTokenInput{
		Token:           localCreate.PairingToken,
		Hostname:        "local.dev",
		Version:         "test-local",
		RunnerPublicKey: "local-pubkey==",
	})
	if err != nil {
		t.Fatalf("consume local runtime: %v", err)
	}
	if _, err := s.TouchAgentDaemonHeartbeat(ctx, store.TouchAgentDaemonHeartbeatInput{
		RuntimeID:          localRuntime.ID,
		DaemonVersion:      "test-local-heartbeat",
		ActiveRequests:     0,
		HeartbeatTimestamp: 1710000300,
		SupportedAgentKinds: []store.AgentDaemonSupportedAgentKind{
			{
				Kind:      "claude_code",
				Available: true,
				Version:   "claude-ok",
				Capabilities: store.AgentDaemonKindCapabilities{
					Streaming:   true,
					Permissions: true,
					Usage:       true,
					Resume:      true,
				},
			},
			{
				Kind:      "opencode",
				Available: true,
				Version:   "opencode-ok",
				Capabilities: store.AgentDaemonKindCapabilities{
					Streaming: true,
					Usage:     true,
				},
			},
		},
	}); err != nil {
		t.Fatalf("heartbeat local runtime: %v", err)
	}

	offlineCreate, err := s.CreateRuntimePairing(ctx, store.CreateRuntimePairingInput{
		WorkspaceID: ids.WorkspaceID,
		Type:        store.RuntimeTypeAgentDaemon,
		Name:        "local-offline",
		Provider:    store.RuntimeProviderAgentDaemon,
		OwnerUserID: ids.UserID,
		ActorID:     ids.UserID,
	})
	if err != nil {
		t.Fatalf("create offline runtime: %v", err)
	}
	if _, err := s.ConsumePairingToken(ctx, store.ConsumePairingTokenInput{
		Token:           offlineCreate.PairingToken,
		Hostname:        "offline.dev",
		Version:         "test-offline",
		RunnerPublicKey: "offline-pubkey==",
	}); err != nil {
		t.Fatalf("consume offline runtime: %v", err)
	}

	sandboxCreate, err := s.CreateRuntimePairing(ctx, store.CreateRuntimePairingInput{
		WorkspaceID: ids.WorkspaceID,
		Type:        store.RuntimeTypeAgentDaemon,
		Name:        "sandbox-daemon",
		Provider:    store.RuntimeProviderAgentDaemonSandbox,
		ActorID:     ids.UserID,
		Config: map[string]any{
			"created_by":   "sandbox_provider",
			"daemon_mode":  "sandbox",
			"sandbox_id":   "sbx_test",
			"sandbox_kind": "agent_daemon_claude_code",
		},
	})
	if err != nil {
		t.Fatalf("create sandbox runtime: %v", err)
	}
	sandboxRuntime, err := s.ConsumePairingToken(ctx, store.ConsumePairingTokenInput{
		Token:           sandboxCreate.PairingToken,
		Hostname:        "sandbox.dev",
		Version:         "test-sandbox",
		RunnerPublicKey: "sandbox-pubkey==",
	})
	if err != nil {
		t.Fatalf("consume sandbox runtime: %v", err)
	}
	if _, err := s.TouchAgentDaemonHeartbeat(ctx, store.TouchAgentDaemonHeartbeatInput{
		RuntimeID:          sandboxRuntime.ID,
		DaemonVersion:      "test-sandbox-heartbeat",
		HeartbeatTimestamp: 1710000400,
		SupportedAgentKinds: []store.AgentDaemonSupportedAgentKind{{
			Kind:      "claude_code",
			Available: true,
		}},
	}); err != nil {
		t.Fatalf("heartbeat sandbox runtime: %v", err)
	}

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			req = req.WithContext(auth.WithUserID(req.Context(), ids.UserID))
			next.ServeHTTP(w, req)
		})
	})
	runtimeapi.RegisterRoutes(r, runtimeapi.Deps{Store: s})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	baseURL := srv.URL + "/api/v1/workspaces/" + ids.WorkspaceID + "/runtimes?type=agent_daemon"
	localOut := getJSON(t, baseURL+"&placement=local_device&liveness=online&agent_kind=claude_code", http.StatusOK)
	localRows := runtimeRows(t, localOut)
	if len(localRows) != 1 || localRows[0]["id"] != localRuntime.ID {
		t.Fatalf("selectable claude local runtimes = %#v, want only %s", localRows, localRuntime.ID)
	}
	cfg, _ := localRows[0]["config"].(map[string]any)
	if _, leaked := cfg["runner_credential_hash"]; leaked {
		t.Fatalf("list DTO leaked runner_credential_hash: %#v", cfg)
	}
	if got := cfg["supported_agent_kind_names"]; got == nil {
		t.Fatalf("selectable local runtime missing supported_agent_kind_names: %#v", cfg)
	}

	opencodeOut := getJSON(t, baseURL+"&placement=local_device&liveness=online&agent_kind=opencode", http.StatusOK)
	if rows := runtimeRows(t, opencodeOut); len(rows) != 1 || rows[0]["id"] != localRuntime.ID {
		t.Fatalf("selectable opencode local runtimes = %#v, want only %s", rows, localRuntime.ID)
	}
	codexOut := getJSON(t, baseURL+"&placement=local_device&liveness=online&agent_kind=codex", http.StatusOK)
	if rows := runtimeRows(t, codexOut); len(rows) != 0 {
		t.Fatalf("selectable codex local runtimes = %#v, want none", rows)
	}
	sandboxOut := getJSON(t, baseURL+"&placement=cloud_sandbox&liveness=online&agent_kind=claude_code", http.StatusOK)
	if rows := runtimeRows(t, sandboxOut); len(rows) != 1 || rows[0]["id"] != sandboxRuntime.ID {
		t.Fatalf("sandbox daemon runtimes = %#v, want only %s", rows, sandboxRuntime.ID)
	}

	respondsWithGet(t, baseURL+"&placement=bogus", http.StatusBadRequest)
	respondsWithGet(t, baseURL+"&liveness=bogus", http.StatusBadRequest)
}

func respondsWithGet(t *testing.T, url string, wantStatus int) {
	t.Helper()
	resp, err := http.DefaultClient.Get(url)
	if err != nil {
		t.Fatalf("get %s: %v", url, err)
	}
	resp.Body.Close()
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s -> %d, want %d", url, resp.StatusCode, wantStatus)
	}
}

func getJSON(t *testing.T, url string, wantStatus int) map[string]any {
	t.Helper()
	resp, err := http.DefaultClient.Get(url)
	if err != nil {
		t.Fatalf("get %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		raw := new(bytes.Buffer)
		_, _ = raw.ReadFrom(resp.Body)
		t.Fatalf("%s -> %d (want %d), body=%s", url, resp.StatusCode, wantStatus, raw.String())
	}
	out := map[string]any{}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode %s: %v", url, err)
	}
	return out
}

func runtimeRows(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()
	items, ok := body["runtimes"].([]any)
	if !ok {
		t.Fatalf("response missing runtimes array: %#v", body)
	}
	rows := make([]map[string]any, 0, len(items))
	for _, item := range items {
		row, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("runtime item is not object: %#v", item)
		}
		rows = append(rows, row)
	}
	return rows
}

func postJSON(t *testing.T, url string, body any, wantStatus int) map[string]any {
	return postJSONBearer(t, url, "", body, wantStatus)
}

func postJSONBearer(t *testing.T, url, bearer string, body any, wantStatus int) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode body: %v", err)
		}
	}
	req, err := http.NewRequest(http.MethodPost, url, &buf)
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		raw := new(bytes.Buffer)
		_, _ = raw.ReadFrom(resp.Body)
		t.Fatalf("%s -> %d (want %d), body=%s", url, resp.StatusCode, wantStatus, raw.String())
	}
	if resp.StatusCode == http.StatusNoContent || resp.ContentLength == 0 {
		return nil
	}
	out := map[string]any{}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil
	}
	return out
}

func respondsWith(t *testing.T, url, bearer string, wantStatus int) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s -> %d, want %d", url, resp.StatusCode, wantStatus)
	}
}

func openTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("PARSAR_TEST_DATABASE_URL"))
	if dsn == "" {
		t.Skip("PARSAR_TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(context.Background(),
		`truncate table runtimes, sandboxes, agent_run_events, usage_logs, audit_records,
		agent_run_artifacts, agent_runs, messages, conversations,
		agents, models, secrets,
		workspace_members, workspaces,
		auth_identities, users restart identity cascade`); err != nil {
		t.Fatal(err)
	}
	return pool
}
