package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Real DB roundtrip — exercises the full runtime lifecycle:
// create pairing -> consume token -> heartbeat -> patch -> sweep ->
// soft-delete. Skipped when PARSAR_TEST_DATABASE_URL is unset.
func TestRuntimeLifecycleRoundTrip(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	ids := DefaultDevFixtureIDs()
	if _, err := New(db).InsertDevFixture(ctx, ids); err != nil {
		t.Fatalf("seed dev fixture: %v", err)
	}

	s := New(db)

	create, err := s.CreateRuntimePairing(ctx, CreateRuntimePairingInput{
		WorkspaceID: ids.WorkspaceID,
		Type:        RuntimeTypeAgentDaemon,
		Name:        "alice-laptop",
		Provider:    RuntimeProviderAgentDaemon,
		OwnerUserID: ids.UserID,
	})
	if err != nil {
		t.Fatalf("create pairing: %v", err)
	}
	if create.Runtime.Liveness != RuntimeLivenessPendingPairing {
		t.Errorf("liveness = %q, want pending_pairing", create.Runtime.Liveness)
	}
	if create.PairingToken == "" {
		t.Error("plaintext token empty")
	}

	// Anti-brute-force regression guard — a leaked token without
	// prefix or with wrong body must not auth.
	if _, err := s.ConsumePairingToken(ctx, ConsumePairingTokenInput{
		Token:           "rtk_doesnotexist",
		Hostname:        "x",
		Version:         "v0",
		RunnerPublicKey: "k",
	}); err != ErrPairingTokenInvalid {
		t.Errorf("wrong token: got err=%v, want ErrPairingTokenInvalid", err)
	}

	// Empty public key is rejected — encryption depends on it.
	if _, err := s.ConsumePairingToken(ctx, ConsumePairingTokenInput{
		Token:           create.PairingToken,
		Hostname:        "x",
		Version:         "v0",
		RunnerPublicKey: "",
	}); err == nil {
		t.Error("empty pubkey: want error, got nil")
	}

	r, err := s.ConsumePairingToken(ctx, ConsumePairingTokenInput{
		Token:           create.PairingToken,
		Hostname:        "alice.local",
		Version:         "0.1.0",
		RunnerPublicKey: "fakepubkeybase64==",
	})
	if err != nil {
		t.Fatalf("consume token: %v", err)
	}
	if r.Liveness != RuntimeLivenessOffline {
		t.Errorf("post-pair liveness = %q, want offline", r.Liveness)
	}
	if r.Hostname != "alice.local" || r.Version != "0.1.0" {
		t.Errorf("hostname/version not persisted: %+v", r)
	}
	if got, _ := r.Config["runner_public_key"].(string); got != "fakepubkeybase64==" {
		t.Errorf("runner_public_key not persisted: config=%+v", r.Config)
	}

	// Re-consume same token must fail (one-shot).
	if _, err := s.ConsumePairingToken(ctx, ConsumePairingTokenInput{
		Token:           create.PairingToken,
		Hostname:        "x",
		Version:         "v",
		RunnerPublicKey: "k",
	}); err != ErrPairingTokenInvalid {
		t.Errorf("re-consume: got err=%v, want ErrPairingTokenInvalid", err)
	}

	status, err := s.TouchRuntimeHeartbeat(ctx, r.ID)
	if err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if status.Liveness != RuntimeLivenessOnline {
		t.Errorf("first heartbeat liveness = %q, want online", status.Liveness)
	}

	got, ok, err := s.GetRuntime(ctx, r.ID)
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.LastHeartbeatAt == nil {
		t.Error("last_heartbeat_at nil after heartbeat")
	}

	// Sweeper with a future cutoff demotes online -> offline
	// (reverse-validates the fix actually fires).
	swept, err := s.SweepStaleRuntimes(ctx, time.Now().Add(1*time.Hour))
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if swept < 1 {
		t.Errorf("sweep swept %d rows, want >=1", swept)
	}
	got, _, _ = s.GetRuntime(ctx, r.ID)
	if got.Liveness != RuntimeLivenessOffline {
		t.Errorf("post-sweep liveness = %q, want offline", got.Liveness)
	}

	list, err := s.ListRuntimes(ctx, ids.WorkspaceID, RuntimeTypeAgentDaemon, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list agent_daemon: got %d, want 1", len(list))
	}

	patched, err := s.PatchRuntime(ctx, PatchRuntimeInput{
		ID:      r.ID,
		NewName: "alice-laptop",
	})
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	if patched.Name != "alice-laptop" {
		t.Errorf("patch not applied: %+v", patched)
	}

	if err := s.SoftDeleteRuntime(ctx, r.ID); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	if _, ok, _ := s.GetRuntime(ctx, r.ID); ok {
		t.Error("runtime still visible after soft delete")
	}
}

// Admin sets config keys at create time; runner pair handshake MUST
// preserve them (jsonb-concat) instead of overwriting.
func TestConsumePairingTokenPreservesAdminConfig(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	ids := DefaultDevFixtureIDs()
	if _, err := New(db).InsertDevFixture(ctx, ids); err != nil {
		t.Fatalf("seed dev fixture: %v", err)
	}
	s := New(db)

	create, err := s.CreateRuntimePairing(ctx, CreateRuntimePairingInput{
		WorkspaceID: ids.WorkspaceID,
		Type:        RuntimeTypeAgentDaemon,
		Name:        "with-admin-config",
		Provider:    RuntimeProviderAgentDaemon,
		OwnerUserID: ids.UserID,
		Config: map[string]any{
			"label":  "datacenter-east",
			"tenant": "acme",
		},
	})
	if err != nil {
		t.Fatalf("create pairing: %v", err)
	}
	if got, _ := create.Runtime.Config["label"].(string); got != "datacenter-east" {
		t.Fatalf("admin config not persisted at create: %v", create.Runtime.Config)
	}

	r, err := s.ConsumePairingToken(ctx, ConsumePairingTokenInput{
		Token:           create.PairingToken,
		Hostname:        "runner-host",
		Version:         "v0",
		RunnerPublicKey: "fakepubkey==",
	})
	if err != nil {
		t.Fatalf("consume pair: %v", err)
	}
	// Pair MUST keep the admin-set keys AND add runner_public_key.
	if got, _ := r.Config["label"].(string); got != "datacenter-east" {
		t.Errorf("admin label lost after pair: config=%v", r.Config)
	}
	if got, _ := r.Config["tenant"].(string); got != "acme" {
		t.Errorf("admin tenant lost after pair: config=%v", r.Config)
	}
	if got, _ := r.Config["runner_public_key"].(string); got != "fakepubkey==" {
		t.Errorf("runner_public_key not persisted: config=%v", r.Config)
	}
}

func TestConsumePairingTokenRejectsEmpty(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, err := New(db).InsertDevFixture(ctx, DefaultDevFixtureIDs()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	s := New(db)
	for _, tok := range []string{"", "   ", "\t\n"} {
		_, err := s.ConsumePairingToken(ctx, ConsumePairingTokenInput{
			Token:           tok,
			Hostname:        "x",
			Version:         "v",
			RunnerPublicKey: "k",
		})
		if err != ErrPairingTokenInvalid {
			t.Errorf("empty token %q: got %v, want ErrPairingTokenInvalid", tok, err)
		}
	}
}

func TestTouchAgentDaemonHeartbeatPersistsCapabilities(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	ids := DefaultDevFixtureIDs()
	if _, err := New(db).InsertDevFixture(ctx, ids); err != nil {
		t.Fatalf("seed dev fixture: %v", err)
	}
	s := New(db)

	create, err := s.CreateRuntimePairing(ctx, CreateRuntimePairingInput{
		WorkspaceID: ids.WorkspaceID,
		Type:        "agent_daemon",
		Name:        "agent-daemon-capability-device",
		Provider:    "agent_daemon",
		OwnerUserID: ids.UserID,
	})
	if err != nil {
		t.Fatalf("create pairing: %v", err)
	}
	runtime, err := s.ConsumePairingToken(ctx, ConsumePairingTokenInput{
		Token:           create.PairingToken,
		Hostname:        "daemon-host.local",
		Version:         "proto-0.1.0",
		RunnerPublicKey: "daemonpubkey==",
	})
	if err != nil {
		t.Fatalf("consume pair: %v", err)
	}

	status, err := s.TouchAgentDaemonHeartbeat(ctx, TouchAgentDaemonHeartbeatInput{
		RuntimeID:          runtime.ID,
		DaemonVersion:      "parsar-daemon-0.2.0",
		ActiveRequests:     4,
		HeartbeatTimestamp: 1710000200,
		SupportedAgentKinds: []AgentDaemonSupportedAgentKind{
			{
				Kind:      "opencode",
				Available: false,
				Version:   "missing",
				Capabilities: AgentDaemonKindCapabilities{
					Streaming: true,
				},
			},
			{
				Kind:      "claude_code",
				Available: true,
				Version:   "1.2.3",
				Capabilities: AgentDaemonKindCapabilities{
					Streaming:   true,
					Permissions: true,
					Usage:       true,
					Resume:      true,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("agent daemon heartbeat: %v", err)
	}
	if status.Liveness != RuntimeLivenessOnline {
		t.Fatalf("heartbeat liveness = %q, want online", status.Liveness)
	}

	got, ok, err := s.GetRuntime(ctx, runtime.ID)
	if err != nil || !ok {
		t.Fatalf("get runtime: ok=%v err=%v", ok, err)
	}
	if got.Version != "parsar-daemon-0.2.0" {
		t.Fatalf("version = %q, want daemon version", got.Version)
	}
	if got.LastHeartbeatAt == nil {
		t.Fatal("last_heartbeat_at nil after agent daemon heartbeat")
	}
	if names := configStringSlice(got.Config["supported_agent_kind_names"]); !equalStringSlice(names, []string{"claude_code"}) {
		t.Fatalf("supported_agent_kind_names = %#v, want [claude_code]", names)
	}
	kindEntries := configObjectSlice(got.Config["supported_agent_kinds"])
	if len(kindEntries) != 2 {
		t.Fatalf("supported_agent_kinds len = %d, want 2: %#v", len(kindEntries), kindEntries)
	}
	if kindEntries[0]["kind"] != "claude_code" || kindEntries[1]["kind"] != "opencode" {
		t.Fatalf("supported_agent_kinds not sorted/preserved: %#v", kindEntries)
	}
	if kindEntries[0]["available"] != true || kindEntries[1]["available"] != false {
		t.Fatalf("available flags not preserved: %#v", kindEntries)
	}
	capabilities := configObject(got.Config["daemon_capabilities"])
	for _, key := range []string{"streaming", "cancellation", "permissions", "usage", "resume"} {
		if capabilities[key] != true {
			t.Fatalf("daemon_capabilities[%s] = %#v, want true; all=%#v", key, capabilities[key], capabilities)
		}
	}
	if capabilities["artifacts"] != false {
		t.Fatalf("daemon_capabilities[artifacts] = %#v, want false", capabilities["artifacts"])
	}
	if got.Config["agent_daemon_active_requests"] != float64(4) {
		t.Fatalf("active requests = %#v, want 4", got.Config["agent_daemon_active_requests"])
	}
	if got.Config["agent_daemon_heartbeat_ts"] != float64(1710000200) {
		t.Fatalf("heartbeat ts = %#v, want 1710000200", got.Config["agent_daemon_heartbeat_ts"])
	}
}

func configStringSlice(v any) []string {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func configObjectSlice(v any) []map[string]any {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func configObject(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestRuntimeLifecycleWritesAuditRecords(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	store, auditIng := newAuditAwareStore(t, db)
	ids := DefaultDevFixtureIDs()
	if _, err := store.InsertDevFixture(ctx, ids); err != nil {
		t.Fatalf("seed dev fixture: %v", err)
	}

	create, err := store.CreateRuntimePairing(ctx, CreateRuntimePairingInput{
		WorkspaceID: ids.WorkspaceID,
		Type:        RuntimeTypeAgentDaemon,
		Name:        "runtime-audit",
		Provider:    RuntimeProviderAgentDaemon,
		OwnerUserID: ids.UserID,
		ActorID:     ids.UserID,
	})
	if err != nil {
		t.Fatalf("create pairing: %v", err)
	}
	runtime, err := store.ConsumePairingToken(ctx, ConsumePairingTokenInput{
		Token:           create.PairingToken,
		Hostname:        "audit.local",
		Version:         "1.2.3",
		RunnerPublicKey: "runtime-audit-pubkey==",
	})
	if err != nil {
		t.Fatalf("consume token: %v", err)
	}
	if _, err := store.TouchRuntimeHeartbeat(ctx, runtime.ID); err != nil {
		t.Fatalf("first heartbeat: %v", err)
	}
	if _, err := store.PatchRuntime(ctx, PatchRuntimeInput{
		ID:      runtime.ID,
		NewName: "runtime-audit-renamed",
		ActorID: ids.UserID,
	}); err != nil {
		t.Fatalf("patch runtime: %v", err)
	}
	if err := store.SoftDeleteRuntimeWithActor(ctx, runtime.ID, ids.UserID); err != nil {
		t.Fatalf("delete runtime: %v", err)
	}

	flushAudit(t, auditIng)

	assertWorkspaceAuditEvent(t, db, ids.WorkspaceID, auditRuntimeCreated, "runtime", create.Runtime.ID)
	assertWorkspaceAuditEvent(t, db, ids.WorkspaceID, auditRuntimePaired, "runtime", runtime.ID)
	assertWorkspaceAuditEvent(t, db, ids.WorkspaceID, auditRuntimeOnline, "runtime", runtime.ID)
	assertWorkspaceAuditEvent(t, db, ids.WorkspaceID, auditRuntimeUpdated, "runtime", runtime.ID)
	assertWorkspaceAuditEvent(t, db, ids.WorkspaceID, auditRuntimeDeleted, "runtime", runtime.ID)
	assertWorkspaceAuditMetadata(t, db, ids.WorkspaceID, auditRuntimeUpdated, "name_to", "runtime-audit-renamed")
	assertWorkspaceAuditMetadata(t, db, ids.WorkspaceID, auditRuntimeOnline, "from_liveness", RuntimeLivenessOffline)

	var leaked int
	if err := db.QueryRow(ctx, `
		select count(*)
		from audit_records
		where workspace_id = $1::uuid
		  and (
			payload::text like '%' || $2 || '%'
			or payload::text like '%' || $3 || '%'
		  )
	`, ids.WorkspaceID, create.PairingToken, "runtime-audit-pubkey==").Scan(&leaked); err != nil {
		t.Fatalf("leak check: %v", err)
	}
	if leaked != 0 {
		t.Fatalf("expected runtime audit payloads to omit pairing token / pubkey, got %d leaked rows", leaked)
	}
}

func TestMarkRuntimeOffline(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	ids := DefaultDevFixtureIDs()
	if _, err := New(db).InsertDevFixture(ctx, ids); err != nil {
		t.Fatalf("seed dev fixture: %v", err)
	}
	s := New(db)

	create, err := s.CreateRuntimePairing(ctx, CreateRuntimePairingInput{
		WorkspaceID: ids.WorkspaceID,
		Type:        RuntimeTypeAgentDaemon,
		Name:        "mark-offline-test",
		Provider:    RuntimeProviderAgentDaemon,
		OwnerUserID: ids.UserID,
	})
	if err != nil {
		t.Fatalf("create pairing: %v", err)
	}
	r, err := s.ConsumePairingToken(ctx, ConsumePairingTokenInput{
		Token:           create.PairingToken,
		Hostname:        "offline.local",
		Version:         "0.1.0",
		RunnerPublicKey: "offlinekey==",
	})
	if err != nil {
		t.Fatalf("consume token: %v", err)
	}
	if _, err := s.TouchRuntimeHeartbeat(ctx, r.ID); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	got, _, _ := s.GetRuntime(ctx, r.ID)
	if got.Liveness != RuntimeLivenessOnline {
		t.Fatalf("pre-mark liveness = %q, want online", got.Liveness)
	}

	if err := s.MarkRuntimeOffline(ctx, r.ID); err != nil {
		t.Fatalf("mark offline: %v", err)
	}
	got, _, _ = s.GetRuntime(ctx, r.ID)
	if got.Liveness != RuntimeLivenessOffline {
		t.Fatalf("post-mark liveness = %q, want offline", got.Liveness)
	}

	// Idempotent: calling again on already-offline runtime doesn't error.
	if err := s.MarkRuntimeOffline(ctx, r.ID); err != nil {
		t.Fatalf("idempotent mark offline: %v", err)
	}
	got, _, _ = s.GetRuntime(ctx, r.ID)
	if got.Liveness != RuntimeLivenessOffline {
		t.Fatalf("idempotent post-mark liveness = %q, want offline", got.Liveness)
	}
}

// Name collisions must surface as ErrRuntimeNameTaken, not the raw
// pg uk_runtimes_workspace_name_active violation text. The API layer
// maps this sentinel to a 409.
func TestCreateRuntimePairing_NameCollision(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()

	ids := DefaultDevFixtureIDs()
	if _, err := New(db).InsertDevFixture(ctx, ids); err != nil {
		t.Fatalf("seed dev fixture: %v", err)
	}
	s := New(db)

	const name = "alice-laptop"
	if _, err := s.CreateRuntimePairing(ctx, CreateRuntimePairingInput{
		WorkspaceID: ids.WorkspaceID,
		Type:        RuntimeTypeAgentDaemon,
		Name:        name,
		Provider:    RuntimeProviderAgentDaemon,
		OwnerUserID: ids.UserID,
	}); err != nil {
		t.Fatalf("first create: %v", err)
	}

	_, err := s.CreateRuntimePairing(ctx, CreateRuntimePairingInput{
		WorkspaceID: ids.WorkspaceID,
		Type:        RuntimeTypeAgentDaemon,
		Name:        name,
		Provider:    RuntimeProviderAgentDaemon,
		OwnerUserID: ids.UserID,
	})
	if !errors.Is(err, ErrRuntimeNameTaken) {
		t.Fatalf("duplicate name: got err=%v, want ErrRuntimeNameTaken", err)
	}
}
