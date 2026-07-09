package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/audit"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestInsertDevFixture(t *testing.T) {
	db := openTestDB(t)

	ctx := context.Background()
	ids := DefaultDevFixtureIDs()

	_, err := New(db).InsertDevFixture(ctx, ids)
	if err != nil {
		t.Fatal(err)
	}

	seededAgain, err := New(db).InsertDevFixture(ctx, ids)
	if err != nil {
		t.Fatal(err)
	}
	if seededAgain.Users != 0 || seededAgain.Workspaces != 0 || seededAgain.Agents != 0 || seededAgain.Conversations != 0 {
		t.Fatalf("expected idempotent second seed, got %+v", seededAgain)
	}

	var count int
	if err := db.QueryRow(ctx, `
		select count(*)
		from agents a
		join workspaces w on w.id = a.workspace_id
		where w.slug = 'demo'
			and a.status = 'active'
			and a.deleted_at is null
			and w.deleted_at is null
	`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("expected 3 active agents, got %d", count)
	}
}

func TestWorkspaceRuntimeSettingsReadsCredentialMask(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ids := DefaultDevFixtureIDs()
	st := New(db)

	if _, err := st.InsertDevFixture(ctx, ids); err != nil {
		t.Fatal(err)
	}
	secret, err := st.CreateSecret(ctx, CreateSecretInput{
		WorkspaceID: ids.WorkspaceID,
		Name:        "E2B Runtime",
		Kind:        "runtime",
		Provider:    "e2b",
		AuthType:    "api_key",
		Masked:      "e2b_•••wxyz",
		CreatedBy:   ids.UserID,
	}, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `
		update workspaces
		set config = jsonb_set(config, '{runtime_credential_secret_id}', to_jsonb($1::text), true),
		    updated_at = now()
		where id = $2
	`, secret.ID, ids.WorkspaceID); err != nil {
		t.Fatal(err)
	}

	settings, err := st.GetWorkspaceRuntimeSettings(ctx, ids.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if settings.RuntimeCredentialSecretID != secret.ID {
		t.Fatalf("RuntimeCredentialSecretID = %q, want %q", settings.RuntimeCredentialSecretID, secret.ID)
	}
	if settings.RuntimeCredentialMasked != "e2b_•••wxyz" {
		t.Fatalf("RuntimeCredentialMasked = %q", settings.RuntimeCredentialMasked)
	}
}

func TestCreateUserCredentialRejectsUnknownKind(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ids := DefaultDevFixtureIDs()
	st := New(db)

	if _, err := st.InsertDevFixture(ctx, ids); err != nil {
		t.Fatal(err)
	}
	_, err := st.CreateUserCredential(ctx, CreateUserCredentialInput{
		UserID:         ids.UserID,
		Kind:           "github_token",
		DisplayName:    "Bad GitHub",
		EncryptedValue: []byte(`{}`),
		KeyVersion:     "v1",
	})
	if !errors.Is(err, ErrInvalidCredentialKind) {
		t.Fatalf("expected ErrInvalidCredentialKind, got %v", err)
	}
}

func TestCreateUserCredentialAcceptsCustomKind(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ids := DefaultDevFixtureIDs()
	st := New(db)

	if _, err := st.InsertDevFixture(ctx, ids); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateCredentialKind(ctx, CreateCredentialKindInput{
		Code:        "gitlab_token",
		DisplayName: "GitLab Token",
		CreatorID:   ids.UserID,
	}); err != nil {
		t.Fatal(err)
	}

	created, err := st.CreateUserCredential(ctx, CreateUserCredentialInput{
		UserID:         ids.UserID,
		Kind:           " GITLAB_TOKEN ",
		DisplayName:    "GitLab",
		EncryptedValue: []byte(`{"token":"encrypted"}`),
		KeyVersion:     "v1",
	})
	if err != nil {
		t.Fatalf("CreateUserCredential custom kind: %v", err)
	}
	if created.Kind != "gitlab_token" {
		t.Fatalf("created.Kind = %q, want gitlab_token", created.Kind)
	}
}

// TestRegisterWorkspaceRuntimeCredentialUpsertOverwritePrior pins that
// a repeat Register transactionally soft-deletes the prior credential
// row so the next insert doesn't trip uk_secrets_workspace_name_active.
func TestRegisterWorkspaceRuntimeCredentialUpsertOverwritePrior(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ids := DefaultDevFixtureIDs()
	st := New(db)

	if _, err := st.InsertDevFixture(ctx, ids); err != nil {
		t.Fatal(err)
	}

	first, err := st.RegisterWorkspaceRuntimeCredential(ctx, RegisterWorkspaceRuntimeCredentialInput{
		WorkspaceID:      ids.WorkspaceID,
		Name:             "Workspace Runtime Credential",
		Kind:             "runtime",
		Provider:         "e2b",
		AuthType:         "api_key",
		EncryptedPayload: []byte(`{"v":1}`),
		Masked:           "e2b_•••aaaa",
		CreatedBy:        ids.UserID,
		Now:              time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if first.ID == "" {
		t.Fatalf("first Register returned empty ID")
	}

	second, err := st.RegisterWorkspaceRuntimeCredential(ctx, RegisterWorkspaceRuntimeCredentialInput{
		WorkspaceID:      ids.WorkspaceID,
		Name:             "Workspace Runtime Credential",
		Kind:             "runtime",
		Provider:         "e2b",
		AuthType:         "api_key",
		EncryptedPayload: []byte(`{"v":2}`),
		Masked:           "e2b_•••bbbb",
		CreatedBy:        ids.UserID,
		Now:              time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("second Register (overwrite path): %v", err)
	}
	if second.ID == first.ID {
		t.Errorf("second Register returned same secret ID %q, want fresh row", second.ID)
	}

	// Workspace pointer must reflect the second secret.
	settings, err := st.GetWorkspaceRuntimeSettings(ctx, ids.WorkspaceID)
	if err != nil {
		t.Fatalf("GetWorkspaceRuntimeSettings: %v", err)
	}
	if settings.RuntimeCredentialSecretID != second.ID {
		t.Errorf("workspace pointer = %q, want second.ID %q", settings.RuntimeCredentialSecretID, second.ID)
	}
	if settings.RuntimeCredentialMasked != "e2b_•••bbbb" {
		t.Errorf("masked preview = %q, want %q", settings.RuntimeCredentialMasked, "e2b_•••bbbb")
	}

	// Old secret row remains in secrets with deleted_at populated
	// (audit trail preserved).
	var deletedAt *time.Time
	if err := db.QueryRow(ctx, `select deleted_at from secrets where id = $1`, first.ID).Scan(&deletedAt); err != nil {
		t.Fatalf("query first secret deleted_at: %v", err)
	}
	if deletedAt == nil {
		t.Errorf("first secret deleted_at is NULL, expected populated soft-delete timestamp")
	}

	// Third call must also succeed — proves the upsert is repeatable.
	if _, err := st.RegisterWorkspaceRuntimeCredential(ctx, RegisterWorkspaceRuntimeCredentialInput{
		WorkspaceID:      ids.WorkspaceID,
		Name:             "Workspace Runtime Credential",
		Kind:             "runtime",
		Provider:         "e2b",
		AuthType:         "api_key",
		EncryptedPayload: []byte(`{"v":3}`),
		Masked:           "e2b_•••cccc",
		CreatedBy:        ids.UserID,
		Now:              time.Now().UTC(),
	}); err != nil {
		t.Fatalf("third Register: %v", err)
	}
}

// TestClearWorkspaceRuntimeCredentialSecretFreesUniqueIndex pins that
// Clear soft-deletes the prior active credential row alongside nulling
// the workspace pointer, so a subsequent Register doesn't collide with
// uk_secrets_workspace_name_active on the (workspace_id, name) slot.
func TestClearWorkspaceRuntimeCredentialSecretFreesUniqueIndex(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ids := DefaultDevFixtureIDs()
	st := New(db)

	if _, err := st.InsertDevFixture(ctx, ids); err != nil {
		t.Fatal(err)
	}

	mkInput := func(version int, masked string) RegisterWorkspaceRuntimeCredentialInput {
		return RegisterWorkspaceRuntimeCredentialInput{
			WorkspaceID:      ids.WorkspaceID,
			Name:             "Workspace Runtime Credential",
			Kind:             "runtime",
			Provider:         "e2b",
			AuthType:         "api_key",
			EncryptedPayload: []byte(fmt.Sprintf(`{"v":%d}`, version)),
			Masked:           masked,
			CreatedBy:        ids.UserID,
			Now:              time.Now().UTC(),
		}
	}

	first, err := st.RegisterWorkspaceRuntimeCredential(ctx, mkInput(1, "e2b_•••aaaa"))
	if err != nil {
		t.Fatalf("first Register: %v", err)
	}

	if err := st.ClearWorkspaceRuntimeCredentialSecret(ctx, ids.WorkspaceID, "Workspace Runtime Credential", "runtime", time.Now().UTC()); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	// Pointer must be empty after Clear.
	settings, err := st.GetWorkspaceRuntimeSettings(ctx, ids.WorkspaceID)
	if err != nil {
		t.Fatalf("GetWorkspaceRuntimeSettings post-Clear: %v", err)
	}
	if settings.RuntimeCredentialSecretID != "" {
		t.Errorf("pointer post-Clear = %q, want empty", settings.RuntimeCredentialSecretID)
	}

	// Prior secret row must be soft-deleted; otherwise the next
	// Register collides with the unique index.
	var firstDeletedAt *time.Time
	if err := db.QueryRow(ctx, `select deleted_at from secrets where id = $1`, first.ID).Scan(&firstDeletedAt); err != nil {
		t.Fatalf("query first secret deleted_at: %v", err)
	}
	if firstDeletedAt == nil {
		t.Fatalf("first secret deleted_at is NULL after Clear — orphan would block next Register")
	}

	if _, err := st.RegisterWorkspaceRuntimeCredential(ctx, mkInput(2, "e2b_•••bbbb")); err != nil {
		t.Fatalf("Register after Clear: %v", err)
	}

	// Clear is idempotent.
	if err := st.ClearWorkspaceRuntimeCredentialSecret(ctx, ids.WorkspaceID, "Workspace Runtime Credential", "runtime", time.Now().UTC()); err != nil {
		t.Fatalf("repeat Clear: %v", err)
	}
	if err := st.ClearWorkspaceRuntimeCredentialSecret(ctx, ids.WorkspaceID, "Workspace Runtime Credential", "runtime", time.Now().UTC()); err != nil {
		t.Fatalf("third Clear (no-op path): %v", err)
	}
}

func TestCreateAgentAcceptsCapabilitiesWithoutWorkspaceAllowlist(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ids := DefaultDevFixtureIDs()
	st := New(db)

	if _, err := st.InsertDevFixture(ctx, ids); err != nil {
		t.Fatal(err)
	}
	created, err := st.CreateAgent(ctx, CreateAgentInput{
		WorkspaceID:   ids.WorkspaceID,
		Name:          "Capability Test Agent",
		ConnectorType: "agent_daemon",
		AgentConfig: map[string]any{
			"daemon_mode": "sandbox",
			"agent_kind":  "opencode",
		},
		Capabilities:    []string{"foo"},
		CapabilitiesSet: true,
		CreatedBy:       ids.UserID,
	})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if got := created.Agent.Capabilities; len(got) != 1 || got[0] != "foo" {
		t.Fatalf("created capabilities = %#v, want [foo]", got)
	}
}

func TestCreateAgentBindsInitialCapabilityVersions(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ids := DefaultDevFixtureIDs()
	st := New(db)

	if _, err := st.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	capability, err := st.CreateCapability(ctx, CreateCapabilityInput{
		WorkspaceID: ids.WorkspaceID,
		CreatorID:   ids.UserID,
		Type:        "skill",
		Name:        "Repo Skill",
		Description: "Loads a repository skill.",
		InitialVersion: &CreateCapabilityVersionInput{
			Version:   "1.0.0",
			CreatorID: ids.UserID,
			Content:   map[string]any{"kind": "skill"},
		},
	})
	if err != nil {
		t.Fatalf("CreateCapability: %v", err)
	}
	versions, err := st.ListCapabilityVersions(ctx, capability.ID)
	if err != nil {
		t.Fatalf("ListCapabilityVersions: %v", err)
	}
	if len(versions) != 1 {
		t.Fatalf("versions = %d, want 1", len(versions))
	}

	created, err := st.CreateAgent(ctx, CreateAgentInput{
		WorkspaceID:   ids.WorkspaceID,
		Name:          "Versioned Skill Agent",
		ConnectorType: "agent_daemon",
		AgentConfig: map[string]any{
			"daemon_mode": "sandbox",
			"agent_kind":  "opencode",
		},
		InitialCapabilities: []InitialAgentCapabilityInput{{
			CapabilityVersionID: versions[0].ID,
			Configuration:       map[string]any{"mode": "create"},
		}},
		CreatedBy: ids.UserID,
	})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}
	if len(created.InitialCapabilities) != 1 {
		t.Fatalf("initial capabilities = %#v, want 1 binding", created.InitialCapabilities)
	}
	binding := created.InitialCapabilities[0]
	if binding.AgentID != created.Agent.ID || binding.CapabilityID != capability.ID || binding.CapabilityVersionID != versions[0].ID {
		t.Fatalf("binding mismatch: %#v", binding)
	}
	if binding.Configuration["mode"] != "create" {
		t.Fatalf("binding configuration = %#v", binding.Configuration)
	}

	listed, err := st.ListAgentCapabilities(ctx, created.Agent.ID)
	if err != nil {
		t.Fatalf("ListAgentCapabilities: %v", err)
	}
	if len(listed) != 1 || listed[0].CapabilityVersionID != versions[0].ID {
		t.Fatalf("persisted capabilities = %#v", listed)
	}
}

func TestCreateAgentAutoAllocatesUniqueSlug(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ids := DefaultDevFixtureIDs()
	st := New(db)

	if _, err := st.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	created, err := st.CreateAgent(ctx, CreateAgentInput{
		WorkspaceID:   ids.WorkspaceID,
		Name:          "Backend Agent",
		ConnectorType: "agent_daemon",
		AgentConfig: map[string]any{
			"daemon_mode": "sandbox",
			"agent_kind":  "opencode",
		},
		CreatedBy: ids.UserID,
	})
	if err != nil {
		t.Fatalf("CreateAgent auto slug: %v", err)
	}
	if !strings.HasPrefix(created.Agent.Slug, "agent-") || len(created.Agent.Slug) != len("agent-")+12 || strings.Trim(created.Agent.Slug[len("agent-"):], "0123456789abcdef") != "" {
		t.Fatalf("auto slug = %q, want agent-<12hex>", created.Agent.Slug)
	}
	if strings.Contains(created.Agent.Slug, "backend-agent") {
		t.Fatalf("auto slug = %q, want random system slug decoupled from name", created.Agent.Slug)
	}

	_, err = st.CreateAgent(ctx, CreateAgentInput{
		WorkspaceID:   ids.WorkspaceID,
		Name:          "Explicit Backend Agent",
		Slug:          "backend-agent",
		ConnectorType: "agent_daemon",
		AgentConfig: map[string]any{
			"daemon_mode": "sandbox",
			"agent_kind":  "opencode",
		},
		CreatedBy: ids.UserID,
	})
	if !errors.Is(err, ErrDuplicateAgentSlug) {
		t.Fatalf("explicit duplicate slug error = %v, want ErrDuplicateAgentSlug", err)
	}
}

func TestSeedDevFixtureReactivatesSeededAgents(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	ids := DefaultDevFixtureIDs()

	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `update agents set status = 'disabled' where id = $1`, ids.BackendAgentID); err != nil {
		t.Fatal(err)
	}

	seededAgain, err := store.SeedDevFixture(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if seededAgain.Agents != 1 {
		t.Fatalf("expected one reactivated agent, got %+v", seededAgain)
	}

	agents, err := store.ListWorkspaceEnabledAgents(ctx, ids.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 3 {
		t.Fatalf("expected seed to restore 3 enabled agents, got %d: %+v", len(agents), agents)
	}

	result, err := store.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{
		ConversationTitle: "demo-group",
		SenderEmail:       "admin@example.com",
		Text:              "@product-agent @backend-agent evaluate the memory module",
		Mentions:          []string{"@product-agent", "@backend-agent"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.RunIDs) != 2 {
		t.Fatalf("expected restored backend agent to be schedulable, got %+v", result)
	}
}

func TestCreateInboundIMMessageCreatesSingleAgentRun(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store, auditIng := newAuditAwareStore(t, db)
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}

	result, err := store.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{
		ConversationTitle: "Demo Group",
		SenderEmail:       "admin@example.com",
		Text:              "@backend-agent check the API",
		Mentions:          []string{"@backend-agent"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.MessageID == "" || len(result.RunIDs) != 1 {
		t.Fatalf("expected message and one run, got %+v", result)
	}

	assertMessageAndRuns(t, db, result.MessageID, 1)
	flushAudit(t, auditIng)
	assertAuditEventCount(t, db, result.WorkspaceID, "", 2)
	assertAuditEvent(t, db, result.WorkspaceID, "im.message.created", "message", result.MessageID)
	assertAuditEvent(t, db, result.WorkspaceID, "agent_run.created", "agent_run", result.RunIDs[0])
	assertAuditMetadataOmitsSensitiveText(t, db, result.WorkspaceID, "check the API", "payload", "command")
}

func TestCreateGatewayMessagePersistsGatewaySource(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store, auditIng := newAuditAwareStore(t, db)
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}

	result, err := store.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{
		ConversationTitle: "Demo Group",
		SenderEmail:       "admin@example.com",
		Text:              "@backend-agent check the API",
		Mentions:          []string{"@backend-agent"},
		Source:            "gateway",
		Gateway:           "feishu",
	})
	if err != nil {
		t.Fatal(err)
	}

	var messageSource, messageGateway, runSource, runGateway string
	if err := db.QueryRow(ctx, `select metadata->>'source', metadata->>'gateway' from messages where id = $1`, result.MessageID).Scan(&messageSource, &messageGateway); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `select metadata->>'source', metadata->>'gateway' from agent_runs where id = $1`, result.RunIDs[0]).Scan(&runSource, &runGateway); err != nil {
		t.Fatal(err)
	}
	if messageSource != "gateway" || messageGateway != "feishu" || runSource != "gateway" || runGateway != "feishu" {
		t.Fatalf("expected gateway source metadata, got message=%s/%s run=%s/%s", messageSource, messageGateway, runSource, runGateway)
	}

	flushAudit(t, auditIng)
	assertAuditEvent(t, db, result.WorkspaceID, "im.message.created", "message", result.MessageID)
	assertAuditMetadata(t, db, result.WorkspaceID, "im.message.created", "source", "gateway")
	assertAuditMetadata(t, db, result.WorkspaceID, "im.message.created", "gateway", "feishu")
}

func TestGatewayMessageResolvesExternalConversationID(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	ids := DefaultDevFixtureIDs()
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	configured, err := store.ConfigureDevConversationExternalRef(ctx, ConfigureDevConversationExternalRefInput{
		ConversationID:   ids.ConversationID,
		Gateway:          "feishu",
		ExternalChatID:   "oc_demo",
		ExternalThreadID: "om_thread",
	})
	if err != nil {
		t.Fatal(err)
	}
	if configured.Platform != "feishu" || configured.ExternalID != "oc_demo" || configured.ExternalThreadID != "om_thread" {
		t.Fatalf("unexpected external conversation config: %+v", configured)
	}

	result, err := store.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{
		SenderEmail:      "admin@example.com",
		Text:             "@backend-agent check the API",
		Mentions:         []string{"@backend-agent"},
		Source:           "gateway",
		Gateway:          "feishu",
		ExternalChatID:   "oc_demo",
		ExternalThreadID: "om_thread",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.WorkspaceID != ids.WorkspaceID {
		t.Fatalf("expected external conversation to resolve demo workspace, got %+v", result)
	}

	var conversationID, externalChatID, externalThreadID string
	if err := db.QueryRow(ctx, `select conversation_id::text, metadata->>'external_chat_id', metadata->>'external_thread_id' from messages where id = $1`, result.MessageID).Scan(&conversationID, &externalChatID, &externalThreadID); err != nil {
		t.Fatal(err)
	}
	if conversationID != ids.ConversationID || externalChatID != "oc_demo" || externalThreadID != "om_thread" {
		t.Fatalf("expected external ids in message metadata, got conversation=%s chat=%s thread=%s", conversationID, externalChatID, externalThreadID)
	}
}

func TestGatewayMessageDedupesExternalMessageID(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	ids := DefaultDevFixtureIDs()
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConfigureDevConversationExternalRef(ctx, ConfigureDevConversationExternalRefInput{ConversationID: ids.ConversationID, Gateway: "feishu", ExternalChatID: "oc_demo"}); err != nil {
		t.Fatal(err)
	}
	input := CreateInboundIMMessageInput{
		SenderEmail:       "admin@example.com",
		Text:              "@backend-agent check the API",
		Mentions:          []string{"@backend-agent"},
		Source:            "gateway",
		Gateway:           "feishu",
		ExternalChatID:    "oc_demo",
		ExternalMessageID: "om_once",
	}
	first, err := store.CreateInboundIMMessage(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateInboundIMMessage(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if first.MessageID != second.MessageID || len(second.RunIDs) != 1 || second.RunIDs[0] != first.RunIDs[0] {
		t.Fatalf("expected duplicate external message to return existing ids, first=%+v second=%+v", first, second)
	}

	var messageCount, runCount int
	if err := db.QueryRow(ctx, `select count(*) from messages where metadata->>'external_message_id' = 'om_once'`).Scan(&messageCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `select count(*) from agent_runs where trigger_message_id = $1`, first.MessageID).Scan(&runCount); err != nil {
		t.Fatal(err)
	}
	if messageCount != 1 || runCount != 1 {
		t.Fatalf("expected one message and one run after dedupe, got messages=%d runs=%d", messageCount, runCount)
	}
}

func TestTargetedFeishuInboundCreatesConversationAndSourceAppOutbound(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	ids := DefaultDevFixtureIDs()
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}

	created, err := store.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{
		Text:              "@feishu-bot implicit @Agent should still route to target Agent",
		Mentions:          []string{"@feishu-bot"},
		Source:            "gateway",
		Gateway:           "feishu",
		ExternalUserID:    "ou_feishu_admin",
		ExternalChatID:    "oc_new_targeted",
		ExternalMessageID: "om_targeted_once",
		TargetAgentID:     ids.BackendAgentID,
		SourceAppID:       "cli_targeted_bot",
		ConversationForm:  "group",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(created.RunIDs) != 1 {
		t.Fatalf("expected one targeted run, got %+v", created)
	}

	var platform, externalID, sourceAppID, primaryAgentID string
	if err := db.QueryRow(ctx, `
		select platform, external_id, source_app_id, metadata->>'primary_agent_id'
		from conversations
		where id = (select conversation_id from messages where id = $1::uuid)
	`, created.MessageID).Scan(&platform, &externalID, &sourceAppID, &primaryAgentID); err != nil {
		t.Fatal(err)
	}
	if platform != "feishu" || externalID != "oc_new_targeted" || sourceAppID != "cli_targeted_bot" || primaryAgentID != ids.BackendAgentID {
		t.Fatalf("unexpected conversation route fields: platform=%s external=%s app=%s primary=%s", platform, externalID, sourceAppID, primaryAgentID)
	}

	completed, err := store.CompleteAgentRun(ctx, CompleteAgentRunInput{RunID: created.RunIDs[0], Source: "agent_daemon", Content: "Bot reply"})
	if err != nil {
		t.Fatal(err)
	}
	var msgSourceAppID, msgChatID string
	if err := db.QueryRow(ctx, `
		select c.source_app_id, c.external_id
		from messages m
		join conversations c on c.id = m.conversation_id
		where m.id = $1::uuid
	`, completed.MessageID).Scan(&msgSourceAppID, &msgChatID); err != nil {
		t.Fatal(err)
	}
	if msgSourceAppID != "cli_targeted_bot" || msgChatID != "oc_new_targeted" {
		t.Fatalf("conversation routing fields = (%q, %q), want (cli_targeted_bot, oc_new_targeted)", msgSourceAppID, msgChatID)
	}
}

func TestFeishuConnectorDiagnosticsSummarizesInboundOutboundState(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	st := New(db)
	ids := DefaultDevFixtureIDs()
	if _, err := st.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}

	unconfigured, err := st.GetFeishuConnectorDiagnostics(ctx, ids.BackendAgentID)
	if err != nil {
		t.Fatalf("GetFeishuConnectorDiagnostics unconfigured: %v", err)
	}
	if unconfigured.Configured || unconfigured.Enabled || unconfigured.ConversationCount != 0 {
		t.Fatalf("expected empty diagnostics before binding, got %+v", unconfigured)
	}

	if _, err := st.UpdateAgentFeishuConnector(ctx, UpdateAgentFeishuConnectorInput{
		AgentID:      ids.BackendAgentID,
		Enabled:      true,
		AppID:        "cli_diag_bot",
		AppSecretRef: "00000000-0000-0000-0000-0000000006a1",
		BotOpenID:    "ou_diag_bot",
		EventMode:    "websocket",
	}, ids.UserID); err != nil {
		t.Fatalf("UpdateAgentFeishuConnector: %v", err)
	}

	completeFromFeishu := func(externalMessageID, content string) string {
		t.Helper()
		created, err := st.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{
			Text:              content,
			Source:            "gateway",
			Gateway:           "feishu",
			ExternalUserID:    "ou_feishu_admin",
			ExternalChatID:    "oc_diag",
			ExternalMessageID: externalMessageID,
			TargetAgentID:     ids.BackendAgentID,
			SourceAppID:       "cli_diag_bot",
			ConversationForm:  "group",
		})
		if err != nil {
			t.Fatalf("CreateInboundIMMessage(%s): %v", externalMessageID, err)
		}
		if len(created.RunIDs) != 1 {
			t.Fatalf("expected one run for %s, got %+v", externalMessageID, created)
		}
		completed, err := st.CompleteAgentRun(ctx, CompleteAgentRunInput{RunID: created.RunIDs[0], Source: "agent_daemon", Content: "reply " + externalMessageID})
		if err != nil {
			t.Fatalf("CompleteAgentRun(%s): %v", externalMessageID, err)
		}
		return completed.MessageID
	}

	deliveredID := completeFromFeishu("om_diag_delivered", "diagnostics delivered")
	if _, err := st.MarkGatewayOutboundDelivered(ctx, MarkGatewayOutboundDeliveredInput{MessageID: deliveredID, DeliveryID: "feishu_delivered_1"}); err != nil {
		t.Fatalf("MarkGatewayOutboundDelivered: %v", err)
	}

	// retrying = conversations.metadata.gateway_inflight.working.attempts > 0
	// dead     = system-notice messages tagged feishu_outbound_dead_letter_*
	_ = completeFromFeishu("om_diag_retrying", "diagnostics retrying")
	var diagConvID string
	if err := db.QueryRow(ctx, `select conversation_id::text from messages where id = $1::uuid`, deliveredID).Scan(&diagConvID); err != nil {
		t.Fatalf("read conversation id: %v", err)
	}
	if _, err := st.UpsertConversationInflightWorkingCard(ctx, UpsertConversationInflightWorkingCardInput{
		ConversationID: diagConvID,
		Slot: WorkingInflightSlot{
			ExternalMsgID:  "om_retry_slot",
			AppID:          "cli_diag_bot",
			ExternalChatID: "oc_diag",
			AgentRunID:     "run-diag-retry",
			SeqEmitted:     1,
			Attempts:       2,
			LastError:      "temporary feishu outage",
			UpdatedAt:      time.Now().UTC(),
		},
	}); err != nil {
		t.Fatalf("seed inflight working slot: %v", err)
	}

	_ = completeFromFeishu("om_diag_dead", "diagnostics dead")
	if _, err := st.SendSystemNoticeMessage(ctx, SendSystemNoticeMessageInput{
		ConversationID: diagConvID,
		Kind:           "feishu_outbound_dead_letter_working_run-diag-dead",
		Content:        "permanent feishu rejection",
		SourceRunID:    "run-diag-dead",
	}); err != nil {
		t.Fatalf("seed dead-letter notice: %v", err)
	}

	_ = completeFromFeishu("om_diag_pending", "diagnostics pending")

	diag, err := st.GetFeishuConnectorDiagnostics(ctx, ids.BackendAgentID)
	if err != nil {
		t.Fatalf("GetFeishuConnectorDiagnostics: %v", err)
	}
	if !diag.Configured || !diag.Enabled || diag.EventMode != "websocket" || !diag.AppIDSet || !diag.AppSecretSet || !diag.BotOpenIDSet {
		t.Fatalf("expected configured websocket connector booleans, got %+v", diag)
	}
	if diag.ConversationCount != 1 || diag.InboundMessageCount != 4 || diag.OutboundMessageCount != 4 {
		t.Fatalf("unexpected message totals: %+v", diag)
	}
	if diag.DeliveredOutboundCount != 1 || diag.RetryingOutboundCount != 1 || diag.DeadOutboundCount != 1 || diag.PendingOutboundCount != 3 {
		t.Fatalf("unexpected delivery counts: %+v", diag)
	}
	if diag.LastInboundAt == nil || diag.LastOutboundAt == nil || diag.LastDeliveredAt == nil || diag.LastErrorAt == nil || diag.LastError == "" {
		t.Fatalf("expected recent timestamps and last error, got %+v", diag)
	}
	// Dead-letter notice content wins over the live inflight slot's
	// last_error: operators care about permanently-failed deliveries first.
	if diag.LastError != "permanent feishu rejection" {
		t.Fatalf("LastError = %q, want %q", diag.LastError, "permanent feishu rejection")
	}
}

func TestGatewayOutboundMessagesAndDelivery(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	ids := DefaultDevFixtureIDs()
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConfigureDevConversationExternalRef(ctx, ConfigureDevConversationExternalRefInput{ConversationID: ids.ConversationID, Gateway: "dev", ExternalChatID: "oc_demo"}); err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{
		ExternalChatID:    "oc_demo",
		SenderEmail:       "admin@example.com",
		Text:              "@backend-agent check the API",
		Mentions:          []string{"@backend-agent"},
		Source:            "gateway",
		Gateway:           "dev",
		ExternalMessageID: "om_outbound",
	})
	if err != nil {
		t.Fatal(err)
	}
	completed, err := store.CompleteAgentRun(ctx, CompleteAgentRunInput{RunID: created.RunIDs[0], Source: "http_agent", Content: "HTTP Agent reply"})
	if err != nil {
		t.Fatal(err)
	}

	// MarkGatewayOutboundDelivered stamps gateway_delivered_at on the
	// messages row so the claim filter skips that conversation next tick;
	// repeat calls must be idempotent on the stamp.
	delivered, err := store.MarkGatewayOutboundDelivered(ctx, MarkGatewayOutboundDeliveredInput{MessageID: completed.MessageID, DeliveryID: "im_delivered_1"})
	if err != nil {
		t.Fatal(err)
	}
	firstDeliveredAt, _ := delivered.Metadata["gateway_delivered_at"].(string)
	if firstDeliveredAt == "" {
		t.Fatalf("expected gateway_delivered_at in metadata, got %+v", delivered.Metadata)
	}
	deliveredAgain, err := store.MarkGatewayOutboundDelivered(ctx, MarkGatewayOutboundDeliveredInput{MessageID: completed.MessageID, DeliveryID: "im_delivered_2"})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := deliveredAgain.Metadata["gateway_delivered_at"].(string); got != firstDeliveredAt {
		t.Fatalf("expected delivered_at stamp to be idempotent, got %q vs first %q", got, firstDeliveredAt)
	}
}

func TestListWorkspaceEnabledAgentsReturnsSeededAgents(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	ids := DefaultDevFixtureIDs()
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}

	agents, err := store.ListWorkspaceEnabledAgents(ctx, ids.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 3 {
		t.Fatalf("expected 3 agents, got %d: %+v", len(agents), agents)
	}
	for _, agent := range agents {
		if agent.Status != "active" || agent.ConnectorType != "agent_daemon" {
			t.Fatalf("expected active agent_daemon agent, got %+v", agent)
		}
	}
}

// TestDaemonExecutionConfigIsAgentScoped pins that daemon execution
// placement is agent scoped; the merged agents.config carries daemon keys
// (daemon_mode, agent_kind) but never a top-level runtime value.
func TestDaemonExecutionConfigIsAgentScoped(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	ids := DefaultDevFixtureIDs()
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}

	created, err := store.CreateAgent(ctx, CreateAgentInput{
		WorkspaceID:   ids.WorkspaceID,
		Name:          "DaemonConfigProbe",
		Description:   "Step 5 daemon config check",
		ConnectorType: "agent_daemon",
		SystemPrompt:  "probe",
		Slug:          "daemon-config-probe",
		AgentConfig: map[string]any{
			"daemon_mode": "sandbox",
			"agent_kind":  "opencode",
			"runtime":     "local",
			"ignored":     "not-persisted",
		},
		CreatedBy: ids.UserID,
	})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	if _, ok := created.Agent.Config["runtime"]; ok {
		t.Fatalf("agents.config must not contain runtime for agent_daemon, got %#v", created.Agent.Config)
	}
	if got := created.Agent.Config["daemon_mode"]; got != "sandbox" {
		t.Fatalf("agents.config daemon_mode = %#v, want sandbox", got)
	}
	if got := created.Agent.Config["agent_kind"]; got != "opencode" {
		t.Fatalf("agents.config agent_kind = %#v, want opencode", got)
	}
	if _, ok := created.Agent.Config["runtime"]; ok {
		t.Fatalf("agents.config must not persist stale runtime key: %#v", created.Agent.Config)
	}
	if _, ok := created.Agent.Config["ignored"]; ok {
		t.Fatalf("agents.config must not persist unknown daemon key: %#v", created.Agent.Config)
	}

	var rawAgentConfig string
	if err := db.QueryRow(ctx, `select config::text from agents where id = $1::uuid`, created.Agent.ID).Scan(&rawAgentConfig); err != nil {
		t.Fatalf("read agents.config: %v", err)
	}
	if strings.Contains(rawAgentConfig, `"runtime"`) {
		t.Fatalf("agents.config must not contain top-level runtime key, got %s", rawAgentConfig)
	}

	enabled, err := store.ListWorkspaceEnabledAgents(ctx, ids.WorkspaceID)
	if err != nil {
		t.Fatalf("ListWorkspaceEnabledAgents: %v", err)
	}
	var sawCreated bool
	for _, row := range enabled {
		if row.AgentID != created.Agent.ID {
			continue
		}
		sawCreated = true
		if row.Runtime != nil {
			t.Fatalf("agent_daemon list row must not expose top-level runtime, got %q", *row.Runtime)
		}
		if got := row.Config["daemon_mode"]; got != "sandbox" {
			t.Fatalf("list row daemon_mode = %#v, want sandbox", got)
		}
		if got := row.Config["agent_kind"]; got != "opencode" {
			t.Fatalf("list row agent_kind = %#v, want opencode", got)
		}
	}
	if !sawCreated {
		t.Fatalf("ListWorkspaceEnabledAgents did not include created Agent %s", created.Agent.ID)
	}
}

func TestConfigureDevAgentConnectorRejectsInvalidInputs(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	ids := DefaultDevFixtureIDs()
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}

	_, err := store.ConfigureDevAgentConnector(ctx, ConfigureDevAgentConnectorInput{AgentID: ids.BackendAgentID, ConnectorType: "bogus"})
	if !errors.Is(err, ErrInvalidConnectorType) {
		t.Fatalf("expected ErrInvalidConnectorType, got %v", err)
	}

	_, err = store.ConfigureDevAgentConnector(ctx, ConfigureDevAgentConnectorInput{AgentID: ids.BackendAgentID, ConnectorType: "opencode_local"})
	if !errors.Is(err, ErrInvalidConnectorType) {
		t.Fatalf("expected ErrInvalidConnectorType for retired opencode_local, got %v", err)
	}

	_, err = store.ConfigureDevAgentConnector(ctx, ConfigureDevAgentConnectorInput{AgentID: "00000000-0000-0000-0000-000000099999", ConnectorType: "http"})
	if !errors.Is(err, ErrUnknownAgent) {
		t.Fatalf("expected ErrUnknownAgent, got %v", err)
	}
}

func TestClaimNextQueuedHTTPAgentRun(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	ids := DefaultDevFixtureIDs()
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConfigureDevAgentConnector(ctx, ConfigureDevAgentConnectorInput{
		AgentID:       ids.BackendAgentID,
		ConnectorType: "http",
		Endpoint:      "http://127.0.0.1:19090/agent",
	}); err != nil {
		t.Fatal(err)
	}

	created, err := store.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{
		ConversationTitle: "Demo Group",
		SenderEmail:       "admin@example.com",
		Text:              "@backend-agent check the API",
		Mentions:          []string{"@backend-agent"},
	})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.ClaimNextQueuedHTTPAgentRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !claim.Claimed || claim.RunID != created.RunIDs[0] {
		t.Fatalf("expected to claim created http run, got %+v", claim)
	}

	var status, claimedBy string
	if err := db.QueryRow(ctx, `select status, metadata->>'claimed_by' from agent_runs where id = $1`, claim.RunID).Scan(&status, &claimedBy); err != nil {
		t.Fatal(err)
	}
	if status != "running" || claimedBy != "http_runner_once" {
		t.Fatalf("expected running claimed run, got status=%s claimed_by=%s", status, claimedBy)
	}

	secondClaim, err := store.ClaimNextQueuedHTTPAgentRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if secondClaim.Claimed {
		t.Fatalf("expected no second queued http run, got %+v", secondClaim)
	}
}

func TestFailAgentRunWritesFailedStatusAndAudit(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store, auditIng := newAuditAwareStore(t, db)
	ids := DefaultDevFixtureIDs()
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConfigureDevAgentConnector(ctx, ConfigureDevAgentConnectorInput{
		AgentID:       ids.BackendAgentID,
		ConnectorType: "http",
		Endpoint:      "http://127.0.0.1:19090/agent",
	}); err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{
		ConversationTitle: "Demo Group",
		SenderEmail:       "admin@example.com",
		Text:              "@backend-agent check the API",
		Mentions:          []string{"@backend-agent"},
	})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.ClaimNextQueuedHTTPAgentRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if claim.RunID != created.RunIDs[0] {
		t.Fatalf("expected claimed run %s, got %+v", created.RunIDs[0], claim)
	}

	if err := store.FailAgentRun(ctx, FailAgentRunInput{RunID: claim.RunID, Source: "http_agent", Reason: "non-2xx"}); err != nil {
		t.Fatal(err)
	}
	var status, failedBy, reason, userFacing string
	var finishedAt pgtype.Timestamptz
	if err := db.QueryRow(ctx, `select status, metadata->>'failed_by', metadata->>'failure_reason', coalesce(metadata->>'user_facing_reason', ''), finished_at from agent_runs where id = $1`, claim.RunID).Scan(&status, &failedBy, &reason, &userFacing, &finishedAt); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || failedBy != "http_agent" || reason != "non-2xx" || !finishedAt.Valid {
		t.Fatalf("expected failed http run, got status=%s failed_by=%s reason=%s finished=%v", status, failedBy, reason, finishedAt.Valid)
	}
	if userFacing == "" {
		t.Fatalf("expected user_facing_reason populated, got empty")
	}
	flushAudit(t, auditIng)
	assertAuditEvent(t, db, ids.WorkspaceID, "http_agent.failed", "agent_run", claim.RunID)

	detail, err := store.GetAgentRun(ctx, claim.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.UserFacingReason != userFacing {
		t.Fatalf("expected detail.UserFacingReason=%q, got %q", userFacing, detail.UserFacingReason)
	}
}

func TestRequeueFailedAgentRun(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store, auditIng := newAuditAwareStore(t, db)
	ids := DefaultDevFixtureIDs()
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConfigureDevAgentConnector(ctx, ConfigureDevAgentConnectorInput{
		AgentID:       ids.BackendAgentID,
		ConnectorType: "http",
		Endpoint:      "http://127.0.0.1:19090/agent",
	}); err != nil {
		t.Fatal(err)
	}
	_, err := store.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{
		ConversationTitle: "Demo Group",
		SenderEmail:       "admin@example.com",
		Text:              "@backend-agent check the API",
		Mentions:          []string{"@backend-agent"},
	})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.ClaimNextQueuedHTTPAgentRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.FailAgentRun(ctx, FailAgentRunInput{RunID: claim.RunID, Source: "http_agent", Reason: "agent down"}); err != nil {
		t.Fatal(err)
	}

	requeued, err := store.RequeueFailedAgentRun(ctx, RequeueAgentRunInput{RunID: claim.RunID, Source: "dev_retry", Reason: "agent recovered"})
	if err != nil {
		t.Fatal(err)
	}
	if requeued.RunID != claim.RunID || requeued.Status != "queued" {
		t.Fatalf("expected queued requeue result, got %+v", requeued)
	}
	var status, requeuedBy, reason string
	var startedAt, finishedAt pgtype.Timestamptz
	if err := db.QueryRow(ctx, `select status, metadata->>'requeued_by', metadata->>'requeue_reason', started_at, finished_at from agent_runs where id = $1`, claim.RunID).Scan(&status, &requeuedBy, &reason, &startedAt, &finishedAt); err != nil {
		t.Fatal(err)
	}
	if status != "queued" || requeuedBy != "dev_retry" || reason != "agent recovered" || startedAt.Valid || finishedAt.Valid {
		t.Fatalf("expected clean queued requeue, got status=%s by=%s reason=%s started=%v finished=%v", status, requeuedBy, reason, startedAt.Valid, finishedAt.Valid)
	}
	flushAudit(t, auditIng)
	assertAuditEvent(t, db, ids.WorkspaceID, "agent_run.requeued", "agent_run", claim.RunID)
}

func TestRequeueFailedAgentRunRejectsNonFailedRun(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{
		ConversationTitle: "Demo Group",
		SenderEmail:       "admin@example.com",
		Text:              "@backend-agent check the API",
		Mentions:          []string{"@backend-agent"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.RequeueFailedAgentRun(ctx, RequeueAgentRunInput{RunID: created.RunIDs[0]})
	if !errors.Is(err, ErrAgentRunNotCompletable) {
		t.Fatalf("expected ErrAgentRunNotCompletable, got %v", err)
	}
}

func TestCancelAgentRunIsIdempotent(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{ConversationTitle: "Demo Group", SenderEmail: "admin@example.com", Text: "@backend-agent check the API", Mentions: []string{"@backend-agent"}})
	if err != nil {
		t.Fatal(err)
	}
	runID := created.RunIDs[0]
	if _, err := db.Exec(ctx, `update agent_runs set status = 'running' where id = $1`, runID); err != nil {
		t.Fatal(err)
	}
	ok, err := store.CancelAgentRun(ctx, runID, "first_cancel")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("first cancel should transition the run")
	}
	ok, err = store.CancelAgentRun(ctx, runID, "second_cancel")
	if err != nil {
		t.Fatalf("second cancel should be no-op, got %v", err)
	}
	if ok {
		t.Fatal("second cancel should report ok=false")
	}
	var status, reason string
	var finishedAt pgtype.Timestamptz
	if err := db.QueryRow(ctx, `select status, failure_reason, finished_at from agent_runs where id = $1`, runID).Scan(&status, &reason, &finishedAt); err != nil {
		t.Fatal(err)
	}
	if status != "cancelled" || reason != "first_cancel" || !finishedAt.Valid {
		t.Fatalf("expected first cancel to stick, got status=%s reason=%s finished=%v", status, reason, finishedAt.Valid)
	}
}

// TestDequeueNextRunPicksOldestQueuedSibling verifies the serial-queue
// hand-off: when a run finishes, the oldest queued sibling on the same
// (conversation, agent) is the next dispatch target.
func TestDequeueNextRunPicksOldestQueuedSibling(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	s := New(db)
	if _, err := s.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	created1, err := s.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{ConversationTitle: "Demo Group", SenderEmail: "admin@example.com", Text: "@backend-agent first", Mentions: []string{"@backend-agent"}})
	if err != nil {
		t.Fatal(err)
	}
	run1ID := created1.RunIDs[0]
	if _, err := db.Exec(ctx, `update agent_runs set status = 'running' where id = $1`, run1ID); err != nil {
		t.Fatal(err)
	}
	// Distinct timestamps so "oldest first" is unambiguous.
	created2, err := s.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{ConversationTitle: "Demo Group", SenderEmail: "admin@example.com", Text: "@backend-agent second", Mentions: []string{"@backend-agent"}})
	if err != nil {
		t.Fatal(err)
	}
	run2ID := created2.RunIDs[0]
	if _, err := db.Exec(ctx, `update agent_runs set created_at = now() - interval '5 seconds' where id = $1`, run2ID); err != nil {
		t.Fatal(err)
	}
	created3, err := s.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{ConversationTitle: "Demo Group", SenderEmail: "admin@example.com", Text: "@backend-agent third", Mentions: []string{"@backend-agent"}})
	if err != nil {
		t.Fatal(err)
	}
	run3ID := created3.RunIDs[0]
	next, err := s.DequeueNextRunForConversationAgent(ctx, run1ID)
	if err != nil {
		t.Fatal(err)
	}
	if next == nil {
		t.Fatal("expected next queued run to be returned, got nil")
	}
	if next.RunID != run2ID {
		t.Fatalf("dequeued runID = %q, want %q (oldest queued)", next.RunID, run2ID)
	}
	// DequeueNextRun looks at the finished run's agent/conversation,
	// ignoring the finished run's own status.
	if _, err := db.Exec(ctx, `update agent_runs set status = 'completed', finished_at = now() where id = $1`, run2ID); err != nil {
		t.Fatal(err)
	}
	next2, err := s.DequeueNextRunForConversationAgent(ctx, run2ID)
	if err != nil {
		t.Fatal(err)
	}
	if next2 == nil || next2.RunID != run3ID {
		t.Fatalf("dequeue after run2 = %+v, want run3 (%s)", next2, run3ID)
	}
}

// TestHasInflightRunForConversationAgentDetectsSibling verifies the
// fast-path inflight check used by StartConversationRun.
func TestHasInflightRunForConversationAgentDetectsSibling(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	s := New(db)
	if _, err := s.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	created1, err := s.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{ConversationTitle: "Demo Group", SenderEmail: "admin@example.com", Text: "@backend-agent one", Mentions: []string{"@backend-agent"}})
	if err != nil {
		t.Fatal(err)
	}
	run1ID := created1.RunIDs[0]
	created2, err := s.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{ConversationTitle: "Demo Group", SenderEmail: "admin@example.com", Text: "@backend-agent two", Mentions: []string{"@backend-agent"}})
	if err != nil {
		t.Fatal(err)
	}
	run2ID := created2.RunIDs[0]
	inflight, err := s.HasInflightRunForConversationAgent(ctx, run2ID)
	if err != nil {
		t.Fatal(err)
	}
	if inflight {
		t.Fatal("expected no inflight sibling when both are queued, got true")
	}
	if _, err := db.Exec(ctx, `update agent_runs set status = 'running' where id = $1`, run1ID); err != nil {
		t.Fatal(err)
	}
	inflight, err = s.HasInflightRunForConversationAgent(ctx, run2ID)
	if err != nil {
		t.Fatal(err)
	}
	if !inflight {
		t.Fatal("expected inflight sibling detected after run1 marked running, got false")
	}
	// run1 looking at itself does NOT count as inflight (own row excluded).
	inflight, err = s.HasInflightRunForConversationAgent(ctx, run1ID)
	if err != nil {
		t.Fatal(err)
	}
	if inflight {
		t.Fatal("expected no inflight when the only running run is self, got true")
	}
}

// TestMarkAgentRunRunningBlockedByInflightSibling: when a sibling is
// already running, transition is rejected with ErrAgentRunBlockedByQueue
// and the run stays in 'queued' for the sibling's terminator to pick up.
func TestMarkAgentRunRunningBlockedByInflightSibling(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	s := New(db)
	if _, err := s.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	created1, err := s.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{ConversationTitle: "Demo Group", SenderEmail: "admin@example.com", Text: "@backend-agent one", Mentions: []string{"@backend-agent"}})
	if err != nil {
		t.Fatal(err)
	}
	run1ID := created1.RunIDs[0]
	if _, err := db.Exec(ctx, `update agent_runs set status = 'running' where id = $1`, run1ID); err != nil {
		t.Fatal(err)
	}
	created2, err := s.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{ConversationTitle: "Demo Group", SenderEmail: "admin@example.com", Text: "@backend-agent two", Mentions: []string{"@backend-agent"}})
	if err != nil {
		t.Fatal(err)
	}
	run2ID := created2.RunIDs[0]
	_, err = s.MarkAgentRunRunning(ctx, run2ID, created2.ConversationID)
	if !errors.Is(err, ErrAgentRunBlockedByQueue) {
		t.Fatalf("MarkAgentRunRunning error = %v, want ErrAgentRunBlockedByQueue", err)
	}
	var status string
	if err := db.QueryRow(ctx, `select status from agent_runs where id = $1`, run2ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "queued" {
		t.Fatalf("run2 status = %q, want queued (must stay queued when blocked)", status)
	}
}

func TestCancelAgentRunDoesNotCancelCompletedRun(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{ConversationTitle: "Demo Group", SenderEmail: "admin@example.com", Text: "@backend-agent check the API", Mentions: []string{"@backend-agent"}})
	if err != nil {
		t.Fatal(err)
	}
	completed, err := store.CompleteAgentRun(ctx, CompleteAgentRunInput{RunID: created.RunIDs[0], Content: "done"})
	if err != nil {
		t.Fatal(err)
	}
	ok, err := store.CancelAgentRun(ctx, completed.RunID, "too_late")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("completed run should report ok=false")
	}
	var status, reason string
	if err := db.QueryRow(ctx, `select status, failure_reason from agent_runs where id = $1`, completed.RunID).Scan(&status, &reason); err != nil {
		t.Fatal(err)
	}
	if status != "completed" || reason != "" {
		t.Fatalf("completed run should not be cancelled, got status=%s reason=%q", status, reason)
	}
}

func TestCompleteHTTPAgentRunUsesHTTPAuditSource(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store, auditIng := newAuditAwareStore(t, db)
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}

	created, err := store.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{
		ConversationTitle: "Demo Group",
		SenderEmail:       "admin@example.com",
		Text:              "@backend-agent check the API",
		Mentions:          []string{"@backend-agent"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `update agent_runs set connector_type = 'http' where id = $1`, created.RunIDs[0]); err != nil {
		t.Fatal(err)
	}

	completed, err := store.CompleteAgentRun(ctx, CompleteAgentRunInput{
		RunID:   created.RunIDs[0],
		Source:  "http_agent",
		Content: "HTTP Agent completed, @test-agent add tests",
		Usage: UsageInput{
			Provider:     "fake-http-agent",
			Model:        "http-agent-v1",
			InputTokens:  21,
			OutputTokens: 13,
			CostUSD:      0.00021,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	flushAudit(t, auditIng)
	assertAuditEvent(t, db, completed.WorkspaceID, "http_agent.completed", "agent_run", completed.RunID)
	assertAuditEvent(t, db, completed.WorkspaceID, "agent_to_agent.child_run.created", "agent_run", completed.ChildRunIDs[0])

	var completedBy, messageSource, usageSource string
	if err := db.QueryRow(ctx, `select metadata->>'completed_by' from agent_runs where id = $1`, completed.RunID).Scan(&completedBy); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `select metadata->>'source' from messages where id = $1`, completed.MessageID).Scan(&messageSource); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `select raw->>'source' from usage_logs where agent_run_id = $1`, completed.RunID).Scan(&usageSource); err != nil {
		t.Fatal(err)
	}
	if completedBy != "http_agent" || messageSource != "http_agent" || usageSource != "http_agent" {
		t.Fatalf("expected http_agent source markers, got completed_by=%s message=%s usage=%s", completedBy, messageSource, usageSource)
	}
}

func TestCompleteAgentRunWritesEmptyUsageWhenNoneReported(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}

	created, err := store.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{
		ConversationTitle: "Demo Group",
		SenderEmail:       "admin@example.com",
		Text:              "@backend-agent check the API",
		Mentions:          []string{"@backend-agent"},
	})
	if err != nil {
		t.Fatal(err)
	}
	completed, err := store.CompleteAgentRun(ctx, CompleteAgentRunInput{RunID: created.RunIDs[0]})
	if err != nil {
		t.Fatal(err)
	}
	// Persist honest empty/zero values when the connector reported no usage
	// — no fabricated provider, model, token, or cost numbers.
	if completed.Usage.Provider != "" || completed.Usage.Model != "" || completed.Usage.InputTokens != 0 || completed.Usage.OutputTokens != 0 || completed.Usage.CostUSD != 0 {
		t.Fatalf("expected empty usage in result, got %+v", completed.Usage)
	}

	usage, err := store.ListWorkspaceUsageLogs(ctx, completed.WorkspaceID, completed.RunID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(usage) != 1 || usage[0].AgentRunID != completed.RunID || usage[0].InputTokens != 0 || usage[0].OutputTokens != 0 {
		t.Fatalf("expected empty usage readable by workspace/run, got %+v", usage)
	}
}

func TestCompleteAgentRunMentionCreatesChildRun(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store, auditIng := newAuditAwareStore(t, db)
	ids := DefaultDevFixtureIDs()
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}

	created, err := store.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{
		ConversationTitle: "Demo Group",
		SenderEmail:       "admin@example.com",
		Text:              "@backend-agent check the API",
		Mentions:          []string{"@backend-agent"},
	})
	if err != nil {
		t.Fatal(err)
	}

	completed, err := store.CompleteAgentRun(ctx, CompleteAgentRunInput{
		RunID:   created.RunIDs[0],
		Content: "I'm done here, @test-agent add regression cases",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(completed.ChildRunIDs) != 1 || len(completed.SkippedMentions) != 0 {
		t.Fatalf("expected one child run and no skips, got %+v", completed)
	}

	// trigger_source='agent' (who initiated it) + trigger_channel='internal'
	// (synthesized server-side by the mention resolver).
	var triggerSource, triggerChannel, requestedByType, requestedByID, triggerMessageID, agentID, status string
	if err := db.QueryRow(ctx, `
		select trigger_source, trigger_channel, requested_by_type, requested_by_id::text, trigger_message_id::text, agent_id::text, status
		from agent_runs
		where id = $1
	`, completed.ChildRunIDs[0]).Scan(&triggerSource, &triggerChannel, &requestedByType, &requestedByID, &triggerMessageID, &agentID, &status); err != nil {
		t.Fatal(err)
	}
	if triggerSource != "agent" || triggerChannel != "internal" || requestedByType != "agent" || requestedByID != ids.BackendAgentID || triggerMessageID != completed.MessageID || agentID != ids.TestAgentID || status != "queued" {
		t.Fatalf("unexpected child run fields: trigger=%s/%s requested=%s/%s trigger_msg=%s agent=%s status=%s", triggerSource, triggerChannel, requestedByType, requestedByID, triggerMessageID, agentID, status)
	}
	flushAudit(t, auditIng)
	assertAuditEvent(t, db, completed.WorkspaceID, "agent_to_agent.child_run.created", "agent_run", completed.ChildRunIDs[0])
}

func TestCompleteAgentRunSelfTriggerSkipped(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	ids := DefaultDevFixtureIDs()
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}

	created, err := store.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{
		ConversationTitle: "Demo Group",
		SenderEmail:       "admin@example.com",
		Text:              "@backend-agent check the API",
		Mentions:          []string{"@backend-agent"},
	})
	if err != nil {
		t.Fatal(err)
	}

	completed, err := store.CompleteAgentRun(ctx, CompleteAgentRunInput{RunID: created.RunIDs[0], Content: "@backend-agent I'll keep going"})
	if err != nil {
		t.Fatal(err)
	}
	if len(completed.ChildRunIDs) != 0 || len(completed.SkippedMentions) != 1 || completed.SkippedMentions[0].Reason != "self_trigger" {
		t.Fatalf("expected self trigger skip, got %+v", completed)
	}

	assertNoChildRuns(t, db, completed.MessageID, ids.BackendAgentID)
}

func TestCompleteAgentRunDuplicateTargetSkipped(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	ids := DefaultDevFixtureIDs()
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}

	created, err := store.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{
		ConversationTitle: "Demo Group",
		SenderEmail:       "admin@example.com",
		Text:              "@backend-agent check the API",
		Mentions:          []string{"@backend-agent"},
	})
	if err != nil {
		t.Fatal(err)
	}

	completed, err := store.CompleteAgentRun(ctx, CompleteAgentRunInput{RunID: created.RunIDs[0], Content: "@TestAgent @test-agent both take a look"})
	if err != nil {
		t.Fatal(err)
	}
	if len(completed.ChildRunIDs) != 1 || len(completed.SkippedMentions) != 1 || completed.SkippedMentions[0].Reason != "duplicate_target" {
		t.Fatalf("expected one child run and one duplicate skip, got %+v", completed)
	}

	var childRuns int
	if err := db.QueryRow(ctx, `select count(*) from agent_runs where trigger_message_id = $1 and agent_id = $2`, completed.MessageID, ids.TestAgentID).Scan(&childRuns); err != nil {
		t.Fatal(err)
	}
	if childRuns != 1 {
		t.Fatalf("expected one child run for duplicate target, got %d", childRuns)
	}
}

func TestConversationTimelineShowsMessagesAndRunStatus(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	ids := DefaultDevFixtureIDs()
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}

	created, err := store.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{
		ConversationTitle: "Demo Group",
		SenderEmail:       "admin@example.com",
		Text:              "@backend-agent check the API",
		Mentions:          []string{"@backend-agent"},
	})
	if err != nil {
		t.Fatal(err)
	}
	completed, err := store.CompleteAgentRun(ctx, CompleteAgentRunInput{RunID: created.RunIDs[0], Content: "backend agent fake runtime output"})
	if err != nil {
		t.Fatal(err)
	}

	timeline, err := store.GetConversationTimeline(ctx, ids.ConversationID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(timeline.Messages) != 2 {
		t.Fatalf("expected user and agent messages, got %+v", timeline.Messages)
	}
	if timeline.Messages[0].ID != created.MessageID || timeline.Messages[0].SenderType != "user" || len(timeline.Messages[0].Runs) != 1 {
		t.Fatalf("expected first timeline message with attached run, got %+v", timeline.Messages[0])
	}
	if timeline.Messages[0].Runs[0].Status != "completed" || timeline.Messages[0].Runs[0].OutputMessageID != completed.MessageID {
		t.Fatalf("expected completed attached run, got %+v", timeline.Messages[0].Runs[0])
	}
	if timeline.Messages[1].ID != completed.MessageID || timeline.Messages[1].SenderType != "agent" {
		t.Fatalf("expected second timeline agent output, got %+v", timeline.Messages[1])
	}
}

func TestConversationTimelineIncludesFailedRunReason(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	ids := DefaultDevFixtureIDs()
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}

	sent, err := store.SendUserMessageToConversation(ctx, SendUserMessageToConversationInput{
		ConversationID:    ids.ConversationID,
		UserID:            ids.UserID,
		Content:           "@product-agent check why it failed",
		MentionedAgentIDs: []string{ids.ProductAgentID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sent.RunIDs) != 1 {
		t.Fatalf("expected one run, got %+v", sent.RunIDs)
	}
	runID := sent.RunIDs[0]
	if _, err := store.MarkAgentRunRunning(ctx, runID, ids.ConversationID); err != nil {
		t.Fatal(err)
	}
	if err := store.FailAgentRun(ctx, FailAgentRunInput{RunID: runID, Source: "agent_daemon", Reason: "opencode exec exit status 2"}); err != nil {
		t.Fatal(err)
	}

	timeline, err := store.GetConversationTimeline(ctx, ids.ConversationID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(timeline.Messages) == 0 || timeline.Messages[0].ID != sent.Message.ID || len(timeline.Messages[0].Runs) != 1 {
		t.Fatalf("expected trigger message with attached failed run, got %+v", timeline.Messages)
	}
	run := timeline.Messages[0].Runs[0]
	if run.ID != runID || run.Status != "failed" {
		t.Fatalf("expected attached failed run %s, got %+v", runID, run)
	}
	if run.UserFacingReason != "Agent local execution failed. Please expand this run's error details for the cause." {
		t.Fatalf("expected user-facing failure reason on attached run, got %+v", run)
	}
}

func TestConversationTimelineAndRunDetailShowAgentHandoff(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	ids := DefaultDevFixtureIDs()
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}

	created, err := store.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{
		ConversationTitle: "Demo Group",
		SenderEmail:       "admin@example.com",
		Text:              "@backend-agent check the API",
		Mentions:          []string{"@backend-agent"},
	})
	if err != nil {
		t.Fatal(err)
	}
	completed, err := store.CompleteAgentRun(ctx, CompleteAgentRunInput{RunID: created.RunIDs[0], Content: "@test-agent add tests"})
	if err != nil {
		t.Fatal(err)
	}

	timeline, err := store.GetConversationTimeline(ctx, ids.ConversationID, 100)
	if err != nil {
		t.Fatal(err)
	}
	var childBrief *AgentRunBriefRead
	for i := range timeline.AgentRuns {
		if timeline.AgentRuns[i].ID == completed.ChildRunIDs[0] {
			childBrief = &timeline.AgentRuns[i]
			break
		}
	}
	if childBrief == nil || childBrief.TriggerMessageID != completed.MessageID || childBrief.Status != "queued" {
		t.Fatalf("expected timeline child run triggered by output message, got %+v", timeline.AgentRuns)
	}

	detail, err := store.GetAgentRun(ctx, completed.ChildRunIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if detail.TriggerMessageID != completed.MessageID || detail.RequestedByType != "agent" || detail.RequestedByID != ids.BackendAgentID {
		t.Fatalf("expected child run detail with output trigger and requester, got %+v", detail)
	}
}

func TestGetAgentRunDetailShowsCompletedOutputMessage(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	ids := DefaultDevFixtureIDs()
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}

	created, err := store.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{
		ConversationTitle: "Demo Group",
		SenderEmail:       "admin@example.com",
		Text:              "@backend-agent check the API",
		Mentions:          []string{"@backend-agent"},
	})
	if err != nil {
		t.Fatal(err)
	}
	completed, err := store.CompleteAgentRun(ctx, CompleteAgentRunInput{RunID: created.RunIDs[0], Content: "backend agent fake runtime output"})
	if err != nil {
		t.Fatal(err)
	}

	runtimeID := newID()
	managedModelID := "00000000-0000-0000-0000-00000000d0d1"
	agentConfig := fmt.Sprintf(
		`{"daemon_mode":"local","agent_kind":"opencode","device_id":"%s","work_dir":"/Users/test/work/parsar","model_id":"%s"}`,
		runtimeID,
		managedModelID,
	)
	// Truncate to microseconds because PostgreSQL `timestamptz` stores
	// 6 fractional-second digits (no nanoseconds). A raw time.Now() carries
	// nanoseconds that get rounded on insert, so the post-read .Equal(now)
	// assertion below would fail on runs where the dropped sub-microsecond
	// bits would have rounded differently.
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := db.Exec(ctx,
		`insert into runtimes(id, workspace_id, type, name, liveness, provider, version, hostname, config, last_heartbeat_at, created_at, updated_at)
		 values ($1::uuid, $2::uuid, 'agent_daemon', 'Mac Mini Runner', 'online', 'agent_daemon', '1.2.3', 'test.local', '{"supported_agent_kinds":["claude_code","opencode"],"daemon_capabilities":{"streaming":true,"permissions":true,"usage":true,"resume":false,"artifacts":false}}'::jsonb, $3, $3, $3)`,
		runtimeID, ids.WorkspaceID, now,
	); err != nil {
		t.Fatalf("seed run detail runtime: %v", err)
	}
	if _, err := db.Exec(ctx,
		`update agents set config = $2::jsonb where id = $1::uuid`,
		completed.AgentID, agentConfig,
	); err != nil {
		t.Fatalf("seed agent runtime config: %v", err)
	}
	if _, err := db.Exec(ctx,
		`insert into connector_session_bindings(conversation_id, connector_type, binding_key, upstream_session_id, metadata, created_at, last_active_at)
		 values ($1, 'agent_daemon', $2, $3, '{"agent_kind":"claude_code","work_dir":"/binding/workdir","sandbox_id":"sbx-known"}'::jsonb, $4, $4)`,
		completed.ConversationID, completed.AgentID, runtimeID, now,
	); err != nil {
		t.Fatalf("seed connector binding metadata: %v", err)
	}
	if _, err := db.Exec(ctx,
		`update agent_runs set runtime_id = $1::uuid, working_directory = $2 where id = $3::uuid`,
		runtimeID, "/Users/test/work/parsar", completed.RunID,
	); err != nil {
		t.Fatalf("bind runtime to run: %v", err)
	}

	detail, err := store.GetAgentRun(ctx, completed.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Status != "completed" || detail.OutputMessage == nil || detail.OutputMessage.ID != completed.MessageID {
		t.Fatalf("expected completed detail with output message, got %+v", detail)
	}
	if detail.OutputMessage.Content != "backend agent fake runtime output" || len(detail.Artifacts) != 0 || len(detail.Usage) != 1 {
		t.Fatalf("expected output content, empty artifacts, and usage, got %+v", detail)
	}
	if detail.Usage[0].AgentRunID != completed.RunID || detail.Usage[0].Provider != "" {
		t.Fatalf("expected run detail usage row with empty provider for completed run, got %+v", detail.Usage)
	}
	if detail.Runtime == nil {
		t.Fatalf("expected runtime snapshot on run detail, got %+v", detail)
	}
	if detail.Runtime.ID != runtimeID || detail.Runtime.Name != "Mac Mini Runner" || detail.Runtime.Type != "agent_daemon" {
		t.Fatalf("unexpected runtime identity: %+v", detail.Runtime)
	}
	if detail.Runtime.Provider != "agent_daemon" || detail.Runtime.Liveness != "online" {
		t.Fatalf("unexpected runtime status: %+v", detail.Runtime)
	}
	if detail.Runtime.WorkingDirectory != "/Users/test/work/parsar" || detail.Runtime.Hostname != "test.local" || detail.Runtime.Version != "1.2.3" {
		t.Fatalf("unexpected runtime metadata: %+v", detail.Runtime)
	}
	if detail.Runtime.ConnectorType != "agent_daemon" || detail.Runtime.AgentKind != "opencode" || detail.Runtime.RuntimeMode != "local" {
		t.Fatalf("unexpected runtime execution axes: %+v", detail.Runtime)
	}
	if detail.Runtime.ExecutionPlace != "local_device" || detail.Runtime.GovernanceMode != "external_byo" {
		t.Fatalf("unexpected derived runtime governance axes: %+v", detail.Runtime)
	}
	if detail.Runtime.Capabilities["streaming"] != true || detail.Runtime.Capabilities["permissions"] != true || detail.Runtime.Capabilities["usage"] != true || detail.Runtime.Capabilities["resume"] != false {
		t.Fatalf("unexpected fallback runtime capabilities: %+v", detail.Runtime.Capabilities)
	}
	if detail.Runtime.CapturedAt != nil {
		t.Fatalf("fallback runtime read should not have captured_at, got %+v", detail.Runtime)
	}
	if detail.Runtime.DeviceID != runtimeID || detail.Runtime.SandboxID != "sbx-known" || detail.Runtime.ManagedModelID != managedModelID {
		t.Fatalf("unexpected runtime execution identifiers: %+v", detail.Runtime)
	}
	if detail.Runtime.LastHeartbeatAt == nil || !detail.Runtime.LastHeartbeatAt.Equal(now) {
		t.Fatalf("unexpected runtime heartbeat: %+v", detail.Runtime)
	}
}

func TestRecordAgentRunExecutionSnapshotFreezesRuntimeRead(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	ids := DefaultDevFixtureIDs()
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}

	created, err := store.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{
		ConversationTitle: "Demo Group",
		SenderEmail:       "admin@example.com",
		Text:              "@backend-agent record run snapshot",
		Mentions:          []string{"@backend-agent"},
	})
	if err != nil {
		t.Fatal(err)
	}
	runID := created.RunIDs[0]
	runtimeID := newID()
	managedModelID := "00000000-0000-0000-0000-00000000d0d2"
	snapshotHeartbeat := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	if _, err := db.Exec(ctx,
		`insert into runtimes(id, workspace_id, type, name, liveness, provider, version, hostname, config, last_heartbeat_at, created_at, updated_at)
		 values ($1::uuid, $2::uuid, 'agent_daemon', 'Snapshot Runner', 'online', 'agent_daemon', '1.0.0', 'snapshot.local', '{"daemon_capabilities":{"streaming":true,"permissions":true,"usage":true,"resume":false}}'::jsonb, $3, $3, $3)`,
		runtimeID, ids.WorkspaceID, snapshotHeartbeat,
	); err != nil {
		t.Fatalf("seed snapshot runtime: %v", err)
	}

	if err := store.RecordAgentRunExecutionSnapshot(ctx, RecordAgentRunExecutionSnapshotInput{
		RunID:            runID,
		ConnectorType:    "agent_daemon",
		RuntimeID:        runtimeID,
		DeviceID:         runtimeID,
		AgentKind:        "opencode",
		RuntimeMode:      "local",
		WorkingDirectory: "/snapshot/workdir",
		ManagedModelID:   managedModelID,
		Capabilities: map[string]bool{
			"streaming":    true,
			"permissions":  true,
			"usage":        true,
			"resume":       false,
			"cancellation": true,
		},
	}); err != nil {
		t.Fatalf("record execution snapshot: %v", err)
	}

	mutatedHeartbeat := snapshotHeartbeat.Add(10 * time.Minute)
	if _, err := db.Exec(ctx,
		`update runtimes
		 set name = 'Mutated Runner', liveness = 'offline', provider = 'agent_daemon_sandbox', version = '9.9.9', hostname = 'mutated.local',
		     config = '{"daemon_capabilities":{"streaming":false,"permissions":false,"usage":false,"resume":true}}'::jsonb,
		     last_heartbeat_at = $2, updated_at = $2
		 where id = $1::uuid`,
		runtimeID, mutatedHeartbeat,
	); err != nil {
		t.Fatalf("mutate runtime after snapshot: %v", err)
	}
	if _, err := db.Exec(ctx,
		`update agents
		 set config = '{"daemon_mode":"sandbox","agent_kind":"claude_code","device_id":"00000000-0000-0000-0000-00000000aaaa","work_dir":"/mutated/workdir","model_id":"00000000-0000-0000-0000-00000000bbbb"}'::jsonb
		 where id = $1::uuid`,
		ids.BackendAgentID,
	); err != nil {
		t.Fatalf("mutate agent config after snapshot: %v", err)
	}

	detail, err := store.GetAgentRun(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Runtime == nil {
		t.Fatalf("expected runtime snapshot on run detail, got %+v", detail)
	}
	if detail.Runtime.ID != runtimeID || detail.Runtime.Name != "Snapshot Runner" || detail.Runtime.Provider != "agent_daemon" || detail.Runtime.Liveness != "online" {
		t.Fatalf("runtime identity/status should come from frozen snapshot, got %+v", detail.Runtime)
	}
	if detail.Runtime.Hostname != "snapshot.local" || detail.Runtime.Version != "1.0.0" {
		t.Fatalf("runtime host/version should come from frozen snapshot, got %+v", detail.Runtime)
	}
	if detail.Runtime.ConnectorType != "agent_daemon" || detail.Runtime.AgentKind != "opencode" || detail.Runtime.RuntimeMode != "local" {
		t.Fatalf("runtime execution axes should come from frozen snapshot, got %+v", detail.Runtime)
	}
	if detail.Runtime.ExecutionPlace != "local_device" || detail.Runtime.GovernanceMode != "external_byo" {
		t.Fatalf("runtime governance axes should come from frozen snapshot, got %+v", detail.Runtime)
	}
	if detail.Runtime.DeviceID != runtimeID || detail.Runtime.WorkingDirectory != "/snapshot/workdir" || detail.Runtime.ManagedModelID != managedModelID {
		t.Fatalf("runtime execution identifiers should come from frozen snapshot, got %+v", detail.Runtime)
	}
	if detail.Runtime.Capabilities["streaming"] != true || detail.Runtime.Capabilities["permissions"] != true || detail.Runtime.Capabilities["usage"] != true || detail.Runtime.Capabilities["resume"] != false || detail.Runtime.Capabilities["cancellation"] != true {
		t.Fatalf("runtime capabilities should come from frozen snapshot, got %+v", detail.Runtime.Capabilities)
	}
	if detail.Runtime.CapturedAt == nil {
		t.Fatalf("expected captured_at on frozen snapshot, got %+v", detail.Runtime)
	}
	if detail.Runtime.LastHeartbeatAt == nil || !detail.Runtime.LastHeartbeatAt.Equal(snapshotHeartbeat) {
		t.Fatalf("runtime heartbeat should come from frozen snapshot, got %+v want %v", detail.Runtime.LastHeartbeatAt, snapshotHeartbeat)
	}

	var rawMetadata []byte
	if err := db.QueryRow(ctx, `select metadata from agent_runs where id = $1::uuid`, runID).Scan(&rawMetadata); err != nil {
		t.Fatalf("read run metadata: %v", err)
	}
	metadata := decodeJSONMap(rawMetadata)
	snapshot, ok := metadata[agentRunExecutionSnapshotKey].(map[string]any)
	if !ok {
		t.Fatalf("expected execution_snapshot in metadata, got %+v", metadata)
	}
	if snapshot["agent_kind"] != "opencode" || snapshot["execution_place"] != "local_device" || snapshot["governance_mode"] != "external_byo" {
		t.Fatalf("unexpected persisted execution_snapshot axes: %+v", snapshot)
	}
	if value, _ := snapshot["captured_at"].(string); value == "" {
		t.Fatalf("expected captured_at in persisted execution_snapshot, got %+v", snapshot)
	}
}

func TestListWorkspaceUsageLogsUnknownWorkspace(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}

	_, err := store.ListWorkspaceUsageLogs(ctx, "00000000-0000-0000-0000-000000099999", "", 10)
	if !errors.Is(err, ErrUnknownWorkspace) {
		t.Fatalf("expected ErrUnknownWorkspace, got %v", err)
	}
}

func TestListWorkspaceAgentRunsFiltersByStatus(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	ids := DefaultDevFixtureIDs()
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}

	created, err := store.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{
		ConversationTitle: "Demo Group",
		SenderEmail:       "admin@example.com",
		Text:              "@product-agent @backend-agent evaluate the API",
		Mentions:          []string{"@product-agent", "@backend-agent"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteAgentRun(ctx, CompleteAgentRunInput{RunID: created.RunIDs[0], Content: "product agent output"}); err != nil {
		t.Fatal(err)
	}

	queued, err := store.ListWorkspaceAgentRuns(ctx, ids.WorkspaceID, []string{"queued"}, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(queued.Runs) != 1 || queued.Runs[0].Status != "queued" {
		t.Fatalf("expected one queued run, got %+v", queued.Runs)
	}
	if queued.Total != 1 {
		t.Fatalf("expected total=1 for queued filter, got %d", queued.Total)
	}

	completed, err := store.ListWorkspaceAgentRuns(ctx, ids.WorkspaceID, []string{"completed"}, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(completed.Runs) != 1 || completed.Runs[0].Status != "completed" {
		t.Fatalf("expected one completed run, got %+v", completed.Runs)
	}

	// Union filter (running ∨ queued) keeps queued only; completed is excluded.
	union, err := store.ListWorkspaceAgentRuns(ctx, ids.WorkspaceID, []string{"running", "queued"}, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(union.Runs) != 1 || union.Runs[0].Status != "queued" {
		t.Fatalf("expected union to keep queued only, got %+v", union.Runs)
	}
	if union.Total != 1 {
		t.Fatalf("expected union total=1, got %d", union.Total)
	}

	// nil statuses = no filter; verify DESC ordering and pagination.
	first, err := store.ListWorkspaceAgentRuns(ctx, ids.WorkspaceID, nil, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Runs) != 1 {
		t.Fatalf("expected one row at offset=0, got %d", len(first.Runs))
	}
	if first.Total != 2 {
		t.Fatalf("expected total=2 across both rows, got %d", first.Total)
	}
	second, err := store.ListWorkspaceAgentRuns(ctx, ids.WorkspaceID, nil, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Runs) != 1 {
		t.Fatalf("expected one row at offset=1, got %d", len(second.Runs))
	}
	if first.Runs[0].ID == second.Runs[0].ID {
		t.Fatalf("offset=0 and offset=1 returned the same row, paging is broken")
	}
	// Both rows share the same trigger message so created_at may tie;
	// id DESC tie-break still gives a stable order.
	if first.Runs[0].CreatedAt.Before(second.Runs[0].CreatedAt) {
		t.Fatalf("expected DESC order, got first=%s before second=%s", first.Runs[0].CreatedAt, second.Runs[0].CreatedAt)
	}
}

func TestCompleteAgentRunRejectsCompletedRunWithoutDirtyWrite(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}

	created, err := store.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{
		ConversationTitle: "Demo Group",
		SenderEmail:       "admin@example.com",
		Text:              "@backend-agent check the API",
		Mentions:          []string{"@backend-agent"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteAgentRun(ctx, CompleteAgentRunInput{RunID: created.RunIDs[0]}); err != nil {
		t.Fatal(err)
	}

	_, err = store.CompleteAgentRun(ctx, CompleteAgentRunInput{RunID: created.RunIDs[0], Content: "should not persist"})
	if !errors.Is(err, ErrAgentRunNotCompletable) {
		t.Fatalf("expected ErrAgentRunNotCompletable, got %v", err)
	}

	var linkedOutputMessages int
	if err := db.QueryRow(ctx, `
		select count(*)
		from messages m
		join agent_runs r on r.output_message_id = m.id
		where r.id = $1
			and m.metadata->>'source' = 'runtime'
	`, created.RunIDs[0]).Scan(&linkedOutputMessages); err != nil {
		t.Fatal(err)
	}
	if linkedOutputMessages != 1 {
		t.Fatalf("expected repeat completion to leave one linked output message, got %d", linkedOutputMessages)
	}

	var rejectedContentMessages int
	if err := db.QueryRow(ctx, `select count(*) from messages where content = 'should not persist'`).Scan(&rejectedContentMessages); err != nil {
		t.Fatal(err)
	}
	if rejectedContentMessages != 0 {
		t.Fatalf("expected rejected completion content to create no messages, got %d", rejectedContentMessages)
	}
}

func TestCompleteAgentRunRejectsUnknownRun(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}

	_, err := store.CompleteAgentRun(ctx, CompleteAgentRunInput{RunID: "00000000-0000-0000-0000-000000099999"})
	if !errors.Is(err, ErrUnknownAgentRun) {
		t.Fatalf("expected ErrUnknownAgentRun, got %v", err)
	}

	var unknownRunMessages int
	if scanErr := db.QueryRow(ctx, `select count(*) from messages where metadata->>'run_id' = '00000000-0000-0000-0000-000000099999'`).Scan(&unknownRunMessages); scanErr != nil {
		t.Fatal(scanErr)
	}
	if unknownRunMessages != 0 {
		t.Fatalf("expected unknown run to create no messages for that run, got %d", unknownRunMessages)
	}
}

func TestCompleteAgentRunRejectsInvalidAgentRelation(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}

	created, err := store.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{
		ConversationTitle: "Demo Group",
		SenderEmail:       "admin@example.com",
		Text:              "@backend-agent check the API",
		Mentions:          []string{"@backend-agent"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `update agents set status = 'disabled' where id = (select agent_id from agent_runs where id = $1)`, created.RunIDs[0]); err != nil {
		t.Fatal(err)
	}

	_, err = store.CompleteAgentRun(ctx, CompleteAgentRunInput{RunID: created.RunIDs[0]})
	if !errors.Is(err, ErrInvalidAgent) {
		t.Fatalf("expected ErrInvalidAgent, got %v", err)
	}

	var outputMessageID pgtype.Text
	if err := db.QueryRow(ctx, `select output_message_id::text from agent_runs where id = $1`, created.RunIDs[0]).Scan(&outputMessageID); err != nil {
		t.Fatal(err)
	}
	if outputMessageID.Valid {
		t.Fatalf("expected invalid agent relation to leave output_message_id null, got %s", outputMessageID.String)
	}
}

// newAuditAwareStore wires a Store to an in-process audit Ingester.
// Callers MUST call flushAudit before audit_records reads because the
// ingester is asynchronous.
func newAuditAwareStore(t *testing.T, pool *pgxpool.Pool) (*Store, *audit.Ingester) {
	t.Helper()
	sink := audit.NewPostgresSink(sqlc.New(pool))
	ingester := audit.NewIngester(sink, audit.Options{BufferCapacity: 64})
	ingester.Start(context.Background())
	t.Cleanup(func() { _ = ingester.Stop(context.Background()) })
	return New(pool, WithAudit(ingester)), ingester
}

// flushAudit drains the ingester's pending buffer so subsequent
// audit_records reads see every prior Emit.
func flushAudit(t *testing.T, ingester *audit.Ingester) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := ingester.Flush(ctx); err != nil {
		t.Fatalf("flushAudit: %v", err)
	}
}

func openTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := os.Getenv("PARSAR_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("PARSAR_TEST_DATABASE_URL is not set")
	}

	ctx := context.Background()
	db, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	lockConn, err := db.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockConn.Exec(ctx, `select pg_advisory_lock(8675309)`); err != nil {
		lockConn.Release()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = lockConn.Exec(context.Background(), `select pg_advisory_unlock(8675309)`)
		lockConn.Release()
	})

	if err := db.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	resetTestDB(t, db)
	return db
}

func resetTestDB(t *testing.T, db *pgxpool.Pool) {
	t.Helper()
	if _, err := db.Exec(context.Background(), `
		truncate table
			runtimes,
			sandboxes,
			agent_run_events,
			usage_logs,
			audit_records,
			agent_run_artifacts,
			agent_runs,
			messages,
			conversations,
			gateway_sessions,
			agents,
			models,
			secrets,
			workspace_members,
			workspaces,
			auth_identities,
			users
		restart identity cascade
	`); err != nil {
		t.Fatal(err)
	}
}

func assertMessageAndRuns(t *testing.T, db *pgxpool.Pool, messageID string, expectedRuns int) {
	t.Helper()
	ctx := context.Background()

	var messageCount int
	if err := db.QueryRow(ctx, `
		select count(*) from messages
		where id = $1 and metadata->>'source' = 'im'
	`, messageID).Scan(&messageCount); err != nil {
		t.Fatal(err)
	}
	if messageCount != 1 {
		t.Fatalf("expected one persisted fake IM message, got %d", messageCount)
	}

	var runCount int
	if err := db.QueryRow(ctx, `
		select count(*) from agent_runs
		where trigger_message_id = $1
			and status = 'queued'
	`, messageID).Scan(&runCount); err != nil {
		t.Fatal(err)
	}
	if runCount != expectedRuns {
		t.Fatalf("expected %d queued runs, got %d", expectedRuns, runCount)
	}
}

func assertNoChildRuns(t *testing.T, db *pgxpool.Pool, triggerMessageID string, agentID string) {
	t.Helper()
	var childRuns int
	if err := db.QueryRow(context.Background(), `select count(*) from agent_runs where trigger_message_id = $1 and agent_id = $2`, triggerMessageID, agentID).Scan(&childRuns); err != nil {
		t.Fatal(err)
	}
	if childRuns != 0 {
		t.Fatalf("expected no child runs for trigger_message=%s agent=%s, got %d", triggerMessageID, agentID, childRuns)
	}
}

func assertAuditEventCount(t *testing.T, db *pgxpool.Pool, workspaceID string, targetType string, want int) {
	t.Helper()
	ctx := context.Background()
	query := `select count(*) from audit_records where workspace_id = $1::uuid`
	args := []any{workspaceID}
	if targetType != "" {
		query += ` and target_type = $2`
		args = append(args, targetType)
	}
	var count int
	if err := db.QueryRow(ctx, query, args...).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("expected %d audit events for workspace=%s target_type=%q, got %d", want, workspaceID, targetType, count)
	}
}

func assertAuditEvent(t *testing.T, db *pgxpool.Pool, workspaceID string, eventType string, targetType string, targetID string) {
	t.Helper()
	var count int
	if err := db.QueryRow(context.Background(), `
		select count(*)
		from audit_records
		where workspace_id = $1::uuid
			and event_type = $2
			and target_type = $3
			and target_id = $4::uuid
	`, workspaceID, eventType, targetType, targetID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected one audit event %s/%s/%s, got %d", eventType, targetType, targetID, count)
	}
}

func assertAuditMetadata(t *testing.T, db *pgxpool.Pool, workspaceID string, eventType string, key string, want string) {
	t.Helper()
	var got string
	if err := db.QueryRow(context.Background(), `
		select payload->>$3
		from audit_records
		where workspace_id = $1::uuid
			and event_type = $2
		order by occurred_at desc, id desc
		limit 1
	`, workspaceID, eventType, key).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("expected audit payload %s=%q for %s, got %q", key, want, eventType, got)
	}
}

func assertAuditMetadataOmitsSensitiveText(t *testing.T, db *pgxpool.Pool, workspaceID string, forbidden ...string) {
	t.Helper()
	rows, err := db.Query(context.Background(), `select payload::text from audit_records where workspace_id = $1::uuid`, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			t.Fatal(err)
		}
		for _, value := range forbidden {
			if strings.Contains(payload, value) {
				t.Fatalf("expected audit payload to omit %q, got %s", value, payload)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestConfigureAgentProfileChecksModelStatus(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	ids := DefaultDevFixtureIDs()
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}

	model, err := store.CreateModel(ctx, CreateModelInput{
		Name:               "Profile Validity Model",
		ProviderType:       "openai",
		Adapter:            "@ai-sdk/openai",
		BaseURL:            "https://example.test/v1",
		ModelKey:           "profile-validity-1",
		CredentialMode:     "credential_ref",
		CredentialKindCode: "openai_api_key",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Active model should succeed.
	if _, err := store.ConfigureAgentProfile(ctx, ConfigureAgentProfileInput{
		AgentID: ids.BackendAgentID,
		ModelID: model.ID,
	}); err != nil {
		t.Fatalf("expected active model to be accepted, got %v", err)
	}

	if _, err := store.ConfigureAgentProfile(ctx, ConfigureAgentProfileInput{
		AgentID: ids.BackendAgentID,
		ModelID: "00000000-0000-0000-0000-000000099999",
	}); !errors.Is(err, ErrUnknownModel) {
		t.Fatalf("expected ErrUnknownModel for missing model, got %v", err)
	}

	if _, err := store.DisableModel(ctx, ids.WorkspaceID, model.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConfigureAgentProfile(ctx, ConfigureAgentProfileInput{
		AgentID: ids.BackendAgentID,
		ModelID: model.ID,
	}); !errors.Is(err, ErrModelDisabled) {
		t.Fatalf("expected ErrModelDisabled for disabled model, got %v", err)
	}
}

func TestCompleteAgentRunSanitizesMessageAndStoresTranscript(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}

	created, err := store.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{
		ConversationTitle: "Demo Group",
		SenderEmail:       "admin@example.com",
		Text:              "@backend-agent check log cleanup",
		Mentions:          []string{"@backend-agent"},
	})
	if err != nil {
		t.Fatal(err)
	}
	runID := created.RunIDs[0]

	noisy := "> build start\n\x1b[31merror logs are ignorable\x1b[0m\n$ ls -la\nbackend-agent: final answer line 1\nbackend-agent: final answer line 2\n"
	result, err := store.CompleteAgentRun(ctx, CompleteAgentRunInput{
		RunID:   runID,
		Source:  "agent_daemon",
		Content: noisy,
		Usage:   UsageInput{Provider: "opencode", Model: "fake"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.MessageID == "" {
		t.Fatal("expected message id from CompleteAgentRun")
	}

	var messageContent string
	if err := db.QueryRow(ctx, `select content from messages where id = $1`, result.MessageID).Scan(&messageContent); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(messageContent, "\x1b[") {
		t.Fatalf("expected ANSI escape removed from message, got %q", messageContent)
	}
	if strings.Contains(messageContent, "> build start") || strings.Contains(messageContent, "$ ls -la") {
		t.Fatalf("expected build/shell preamble removed, got %q", messageContent)
	}
	if !strings.Contains(messageContent, "final answer line 1") {
		t.Fatalf("expected final answer preserved, got %q", messageContent)
	}

	detail, err := store.GetAgentRun(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Transcript == "" {
		t.Fatal("expected transcript stored on run metadata")
	}
	if !strings.Contains(detail.Transcript, "> build start") || !strings.Contains(detail.Transcript, "\x1b[31m") {
		t.Fatalf("expected raw transcript preserved, got %q", detail.Transcript)
	}
}

func TestCompleteAgentRunSkipsTranscriptWhenAlreadyClean(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}

	created, err := store.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{
		ConversationTitle: "Demo Group",
		SenderEmail:       "admin@example.com",
		Text:              "@backend-agent clean output",
		Mentions:          []string{"@backend-agent"},
	})
	if err != nil {
		t.Fatal(err)
	}
	runID := created.RunIDs[0]
	if _, err := store.CompleteAgentRun(ctx, CompleteAgentRunInput{
		RunID:   runID,
		Source:  "http_agent",
		Content: "backend-agent: a clean final answer.",
		Usage:   UsageInput{Provider: "http", Model: "clean"},
	}); err != nil {
		t.Fatal(err)
	}
	detail, err := store.GetAgentRun(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Transcript != "" {
		t.Fatalf("expected no transcript for clean output, got %q", detail.Transcript)
	}
}

func TestCreateWorkspaceConversationCreatesActiveWebConversation(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	ids := DefaultDevFixtureIDs()
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}

	conv, err := store.CreateWorkspaceConversation(ctx, CreateWorkspaceConversationInput{
		WorkspaceID: ids.WorkspaceID,
		Title:       "  debug API errors  ",
		Metadata:    map[string]any{"source": "demo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if conv.ID == "" || conv.WorkspaceID != ids.WorkspaceID {
		t.Fatalf("unexpected conversation row: %+v", conv)
	}
	if conv.Title != "debug API errors" {
		t.Fatalf("expected title trimmed, got %q", conv.Title)
	}
	if conv.Surface != "web" || conv.Form != "thread" || conv.Status != "active" {
		t.Fatalf("expected web/thread/active defaults, got surface=%s form=%s status=%s", conv.Surface, conv.Form, conv.Status)
	}
	if conv.WorkspaceID != ids.WorkspaceID {
		t.Fatalf("expected conversation to inherit workspace, got %q want %q", conv.WorkspaceID, ids.WorkspaceID)
	}
	if conv.Metadata["source"] != "demo" {
		t.Fatalf("expected metadata persisted, got %+v", conv.Metadata)
	}

	roundtrip, err := store.GetConversation(ctx, conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if roundtrip.ID != conv.ID || roundtrip.WorkspaceID != ids.WorkspaceID {
		t.Fatalf("get returned different row: %+v", roundtrip)
	}
}

func TestRuntimeErrorSystemMessagePersistsStructuredPayload(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	ids := DefaultDevFixtureIDs()
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	conv, err := store.CreateWorkspaceConversation(ctx, CreateWorkspaceConversationInput{
		WorkspaceID:    ids.WorkspaceID,
		Title:          "Runtime credential failure",
		PrimaryAgentID: ids.ProductAgentID,
	})
	if err != nil {
		t.Fatal(err)
	}
	messageID, err := store.CreateRuntimeErrorSystemMessage(ctx, CreateRuntimeErrorSystemMessageInput{
		WorkspaceID:    ids.WorkspaceID,
		AgentID:        ids.ProductAgentID,
		RunID:          "00000000-0000-0000-0000-0000000000aa",
		ConversationID: conv.ID,
		SubKind:        "capability_credential_missing",
		CapabilityID:   "00000000-0000-0000-0000-00000000c001",
		CapabilityName: "GitHub Issue lookup",
		CredentialKind: "github_pat",
	})
	if err != nil {
		t.Fatal(err)
	}

	var senderType, kindColumn, metaKind, subKind, capabilityName, credentialKind, runID string
	if err := db.QueryRow(ctx, `
		select sender_type, kind, metadata->>'kind', metadata->>'sub_kind',
		       metadata->>'capability_name', metadata->>'credential_kind', metadata->>'run_id'
		from messages
		where id = $1
	`, messageID).Scan(&senderType, &kindColumn, &metaKind, &subKind, &capabilityName, &credentialKind, &runID); err != nil {
		t.Fatal(err)
	}
	// Runtime errors persist as kind='error' on the column with
	// metadata.kind='runtime_error' carrying the legacy classification.
	if senderType != "system" || kindColumn != "error" || metaKind != "runtime_error" {
		t.Fatalf("unexpected message classification sender=%s kind=%s metaKind=%s", senderType, kindColumn, metaKind)
	}
	if subKind != "capability_credential_missing" || capabilityName != "GitHub Issue lookup" || credentialKind != "github_pat" || runID != "00000000-0000-0000-0000-0000000000aa" {
		t.Fatalf("unexpected runtime metadata sub=%s cap=%s kind=%s run=%s", subKind, capabilityName, credentialKind, runID)
	}
}

func TestCreateWorkspaceConversationDefaultsAndValidates(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	ids := DefaultDevFixtureIDs()
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}

	conv, err := store.CreateWorkspaceConversation(ctx, CreateWorkspaceConversationInput{WorkspaceID: ids.WorkspaceID})
	if err != nil {
		t.Fatal(err)
	}
	if conv.Title != "Untitled conversation" {
		t.Fatalf("expected default title, got %q", conv.Title)
	}
	if conv.Surface != "web" || conv.Form != "thread" {
		t.Fatalf("expected default surface=web form=thread, got surface=%q form=%q", conv.Surface, conv.Form)
	}

	if _, err := store.CreateWorkspaceConversation(ctx, CreateWorkspaceConversationInput{WorkspaceID: ids.WorkspaceID, Surface: "bogus"}); err == nil {
		t.Fatal("expected invalid surface to be rejected")
	}

	if _, err := store.CreateWorkspaceConversation(ctx, CreateWorkspaceConversationInput{WorkspaceID: "00000000-0000-0000-0000-000000000000"}); !errors.Is(err, ErrUnknownWorkspace) {
		t.Fatalf("expected ErrUnknownWorkspace for missing workspace, got %v", err)
	}
}

func TestCreateWorkspaceConversationBindsPrimaryAgent(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	ids := DefaultDevFixtureIDs()
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}

	conv, err := store.CreateWorkspaceConversation(ctx, CreateWorkspaceConversationInput{
		WorkspaceID:    ids.WorkspaceID,
		Title:          "evaluate file attachment feature",
		PrimaryAgentID: ids.BackendAgentID,
		Metadata:       map[string]any{"source": "demo"},
	})
	if err != nil {
		t.Fatalf("create conversation with primary agent: %v", err)
	}
	if got := conv.Metadata["primary_agent_id"]; got != ids.BackendAgentID {
		t.Fatalf("expected metadata.primary_agent_id=%s, got %v", ids.BackendAgentID, got)
	}
	if conv.Metadata["source"] != "demo" {
		t.Fatalf("expected metadata.source preserved alongside primary_agent_id, got %+v", conv.Metadata)
	}
	if conv.PrimaryAgentID != ids.BackendAgentID {
		t.Fatalf("expected derived PrimaryAgentID=%s, got %q", ids.BackendAgentID, conv.PrimaryAgentID)
	}
	if conv.PrimaryAgentName == "" {
		t.Fatalf("expected derived PrimaryAgentName non-empty for active backend agent, got empty")
	}

	roundtrip, err := store.GetConversation(ctx, conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := roundtrip.Metadata["primary_agent_id"]; got != ids.BackendAgentID {
		t.Fatalf("expected roundtrip metadata.primary_agent_id=%s, got %v", ids.BackendAgentID, got)
	}
	if roundtrip.PrimaryAgentID != ids.BackendAgentID || roundtrip.PrimaryAgentName == "" {
		t.Fatalf("expected roundtrip derived fields populated, got id=%q name=%q", roundtrip.PrimaryAgentID, roundtrip.PrimaryAgentName)
	}
}

func TestCreateWorkspaceConversationRejectsForeignAgent(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	ids := DefaultDevFixtureIDs()
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}

	// Random UUID — not an agent in this workspace.
	_, err := store.CreateWorkspaceConversation(ctx, CreateWorkspaceConversationInput{
		WorkspaceID:    ids.WorkspaceID,
		Title:          "should fail",
		PrimaryAgentID: "00000000-0000-0000-0000-0000000000ff",
	})
	if !errors.Is(err, ErrUnknownMention) {
		t.Fatalf("expected ErrUnknownMention for foreign agent, got %v", err)
	}

	_, err = store.CreateWorkspaceConversation(ctx, CreateWorkspaceConversationInput{
		WorkspaceID:    ids.WorkspaceID,
		Title:          "should fail",
		PrimaryAgentID: "not-a-uuid",
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for malformed primary_agent_id, got %v", err)
	}
}

func TestCreateWorkspaceConversationRejectsReservedMetadataKey(t *testing.T) {
	// primary_agent_id is server-written only; callers must not bypass
	// agent validation by stuffing it into metadata.
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	ids := DefaultDevFixtureIDs()
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}

	_, err := store.CreateWorkspaceConversation(ctx, CreateWorkspaceConversationInput{
		WorkspaceID: ids.WorkspaceID,
		Title:       "should reject metadata bypass",
		Metadata:    map[string]any{"primary_agent_id": ids.BackendAgentID},
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for metadata.primary_agent_id, got %v", err)
	}

	// Also rejected when paired with a valid top-level agent_id — the
	// metadata key alone is the violation.
	_, err = store.CreateWorkspaceConversation(ctx, CreateWorkspaceConversationInput{
		WorkspaceID:    ids.WorkspaceID,
		Title:          "should reject metadata bypass even with top-level",
		PrimaryAgentID: ids.BackendAgentID,
		Metadata:       map[string]any{"primary_agent_id": "anything", "source": "demo"},
	})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for metadata.primary_agent_id even with top-level agent_id, got %v", err)
	}
}

func TestListWorkspaceConversationsOrdersByRecentActivity(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	ids := DefaultDevFixtureIDs()
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}

	// Seeded Demo Group has no messages yet. A fresh empty conversation
	// should sort by created_at desc against Demo Group.
	older, err := store.CreateWorkspaceConversation(ctx, CreateWorkspaceConversationInput{
		WorkspaceID: ids.WorkspaceID,
		Title:       "Older Empty",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{
		ConversationTitle: "Demo Group",
		SenderEmail:       "admin@example.com",
		Text:              "ping",
	}); err != nil {
		t.Fatal(err)
	}

	items, err := store.ListWorkspaceConversations(ctx, ids.WorkspaceID, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) < 2 {
		t.Fatalf("expected at least two conversations, got %d", len(items))
	}
	first := items[0]
	if first.ID != ids.ConversationID {
		t.Fatalf("expected Demo Group at top after message, got %+v", first)
	}
	if first.MessageCount < 1 || first.LastMessageAt == nil || first.LastMessagePreview == "" || first.LastMessageSenderType == "" {
		t.Fatalf("expected populated last_message_* fields, got %+v", first)
	}

	var foundOlder bool
	for _, item := range items {
		if item.ID == older.ID {
			foundOlder = true
			if item.MessageCount != 0 || item.LastMessageAt != nil || item.LastMessagePreview != "" {
				t.Fatalf("expected empty conversation to have zero message metadata, got %+v", item)
			}
		}
	}
	if !foundOlder {
		t.Fatal("expected newly created conversation in list")
	}
}

func TestListWorkspaceConversationsRejectsUnknownWorkspace(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)

	if _, err := store.ListWorkspaceConversations(ctx, "00000000-0000-0000-0000-000000000000", "", 10); !errors.Is(err, ErrUnknownWorkspace) {
		t.Fatalf("expected ErrUnknownWorkspace, got %v", err)
	}
}

// TestUpdateConversationTitleRenamesActiveConversation: title is
// trimmed, persisted, and the rename is idempotent.
func TestUpdateConversationTitleRenamesActiveConversation(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	ids := DefaultDevFixtureIDs()
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	conv, err := store.CreateWorkspaceConversation(ctx, CreateWorkspaceConversationInput{
		WorkspaceID: ids.WorkspaceID,
		Title:       "original title",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateConversationTitle(ctx, conv.ID, "  new title  "); err != nil {
		t.Fatalf("rename: %v", err)
	}
	roundtrip, err := store.GetConversation(ctx, conv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if roundtrip.Title != "new title" {
		t.Fatalf("title after rename = %q, want %q (trimmed)", roundtrip.Title, "new title")
	}
	if err := store.UpdateConversationTitle(ctx, conv.ID, "   "); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("empty title: want ErrInvalidInput, got %v", err)
	}
	if err := store.UpdateConversationTitle(ctx, "00000000-0000-0000-0000-000000000000", "x"); !errors.Is(err, ErrUnknownConversation) {
		t.Fatalf("unknown conv: want ErrUnknownConversation, got %v", err)
	}
}

// TestSoftDeleteConversationHidesFromUserSurfaces: soft-delete sets
// deleted_at and is filtered out of list / get / update; idempotent.
func TestSoftDeleteConversationHidesFromUserSurfaces(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	ids := DefaultDevFixtureIDs()
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	conv, err := store.CreateWorkspaceConversation(ctx, CreateWorkspaceConversationInput{
		WorkspaceID: ids.WorkspaceID,
		Title:       "to be deleted",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Sanity: list includes it before delete.
	before, err := store.ListWorkspaceConversations(ctx, ids.WorkspaceID, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	foundBefore := false
	for _, c := range before {
		if c.ID == conv.ID {
			foundBefore = true
			break
		}
	}
	if !foundBefore {
		t.Fatalf("pre-delete: conversation %s not in workspace list", conv.ID)
	}

	if err := store.SoftDeleteConversation(ctx, conv.ID); err != nil {
		t.Fatalf("first delete: %v", err)
	}
	// Row stays for FK integrity but is invisible to user-facing paths.
	if _, err := store.GetConversation(ctx, conv.ID); !errors.Is(err, ErrUnknownConversation) {
		t.Fatalf("get after delete: want ErrUnknownConversation, got %v", err)
	}
	after, err := store.ListWorkspaceConversations(ctx, ids.WorkspaceID, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range after {
		if c.ID == conv.ID {
			t.Fatalf("post-delete: conversation %s still in workspace list", conv.ID)
		}
	}
	if err := store.UpdateConversationTitle(ctx, conv.ID, "zombie"); !errors.Is(err, ErrUnknownConversation) {
		t.Fatalf("rename after delete: want ErrUnknownConversation, got %v", err)
	}
	if err := store.SoftDeleteConversation(ctx, conv.ID); !errors.Is(err, ErrUnknownConversation) {
		t.Fatalf("second delete: want ErrUnknownConversation, got %v", err)
	}
}

func TestListWorkspaceConversationsFiltersByAgent(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	ids := DefaultDevFixtureIDs()
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}

	// Two new conversations bound to two different agents.
	boundBackend, err := store.CreateWorkspaceConversation(ctx, CreateWorkspaceConversationInput{
		WorkspaceID:    ids.WorkspaceID,
		Title:          "Backend bound conv",
		PrimaryAgentID: ids.BackendAgentID,
	})
	if err != nil {
		t.Fatal(err)
	}
	boundProduct, err := store.CreateWorkspaceConversation(ctx, CreateWorkspaceConversationInput{
		WorkspaceID:    ids.WorkspaceID,
		Title:          "Product bound conv",
		PrimaryAgentID: ids.ProductAgentID,
	})
	if err != nil {
		t.Fatal(err)
	}

	items, err := store.ListWorkspaceConversations(ctx, ids.WorkspaceID, ids.BackendAgentID, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.ID == boundProduct.ID {
			t.Fatalf("filter agent=backend leaked product-bound conv %s", item.ID)
		}
	}
	var foundBackend bool
	for _, item := range items {
		if item.ID == boundBackend.ID {
			foundBackend = true
			if item.PrimaryAgentID != ids.BackendAgentID {
				t.Fatalf("expected list item PrimaryAgentID=%s, got %q", ids.BackendAgentID, item.PrimaryAgentID)
			}
			if item.PrimaryAgentName == "" {
				t.Fatalf("expected list item PrimaryAgentName non-empty, got empty")
			}
		}
	}
	if !foundBackend {
		t.Fatalf("filter agent=backend missed the backend-bound conv %s", boundBackend.ID)
	}

	all, err := store.ListWorkspaceConversations(ctx, ids.WorkspaceID, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	var seenBackend, seenProduct bool
	for _, item := range all {
		if item.ID == boundBackend.ID {
			seenBackend = true
		}
		if item.ID == boundProduct.ID {
			seenProduct = true
		}
	}
	if !seenBackend || !seenProduct {
		t.Fatalf("empty filter missed convs: backend=%v product=%v", seenBackend, seenProduct)
	}

	if _, err := store.ListWorkspaceConversations(ctx, ids.WorkspaceID, "not-a-uuid", 50); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for malformed agent_id, got %v", err)
	}
}

func TestListUserWorkspacesReturnsSeededWorkspace(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	ids := DefaultDevFixtureIDs()

	rows, err := store.ListUserWorkspaces(ctx, ids.UserID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected exactly 1 workspace for seed user, got %d: %+v", len(rows), rows)
	}
	row := rows[0]
	if row.ID != ids.WorkspaceID {
		t.Fatalf("expected workspace id %s, got %s", ids.WorkspaceID, row.ID)
	}
	if row.Name != "Demo Workspace" || row.Slug != "demo" || row.Role != "owner" {
		t.Fatalf("expected name/slug/role, got %+v", row)
	}
}

func TestListUserWorkspacesEmptyForUnknownUser(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}

	rows, err := store.ListUserWorkspaces(ctx, "00000000-0000-0000-0000-0000000000aa", 10)
	if err != nil {
		t.Fatalf("expected no error for unknown user, got %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected empty list for unknown user, got %d: %+v", len(rows), rows)
	}
}

func TestCreateWorkspaceHappyPath(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	st := New(db)
	if _, err := st.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	ids := DefaultDevFixtureIDs()

	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	result, err := st.CreateWorkspace(ctx, CreateWorkspaceInput{
		Name:      "Brand New",
		CreatedBy: ids.UserID,
		Now:       now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Workspace.Name != "Brand New" {
		t.Fatalf("unexpected workspace name: %+v", result.Workspace)
	}
	if !strings.HasPrefix(result.Workspace.Slug, "workspace-") || len(result.Workspace.Slug) != len("workspace-")+12 || strings.Trim(result.Workspace.Slug[len("workspace-"):], "0123456789abcdef") != "" {
		t.Fatalf("expected auto slug 'workspace-<12hex>', got %q", result.Workspace.Slug)
	}
	if result.Member.Role != "owner" || result.Member.UserID != ids.UserID {
		t.Fatalf("expected owner member for creator, got %+v", result.Member)
	}

	// New workspace should now show up in the creator's workspace list
	rows, err := st.ListUserWorkspaces(ctx, ids.UserID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 workspaces (seed + new), got %d: %+v", len(rows), rows)
	}
}

func TestCreateWorkspaceWithCJKNameStillSucceeds(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	st := New(db)
	if _, err := st.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	ids := DefaultDevFixtureIDs()

	result, err := st.CreateWorkspace(ctx, CreateWorkspaceInput{
		Name:      "中文工作区",
		CreatedBy: ids.UserID,
		Now:       time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Workspace.Name != "中文工作区" {
		t.Fatalf("expected CJK name preserved, got %q", result.Workspace.Name)
	}
	if !strings.HasPrefix(result.Workspace.Slug, "workspace-") {
		t.Fatalf("expected auto slug, got %q", result.Workspace.Slug)
	}
}

func TestBootstrapUserWorkspaceHappyPath(t *testing.T) {
	t.Skip("BootstrapUserWorkspace removed; onboarding now uses CreateWorkspace")
}

func TestCreateWorkspaceRejectsEmptyName(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	st := New(db)
	if _, err := st.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	ids := DefaultDevFixtureIDs()

	if _, err := st.CreateWorkspace(ctx, CreateWorkspaceInput{
		Name:      "   ",
		CreatedBy: ids.UserID,
		Now:       time.Now().UTC(),
	}); !errors.Is(err, ErrInvalidWorkspaceInput) {
		t.Fatalf("expected ErrInvalidWorkspaceInput, got %v", err)
	}
}

func TestUpdateWorkspaceRenames(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	st := New(db)
	if _, err := st.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	ids := DefaultDevFixtureIDs()

	newName := "Renamed Demo"
	row, err := st.UpdateWorkspace(ctx, UpdateWorkspaceInput{
		WorkspaceID: ids.WorkspaceID,
		Name:        &newName,
		ActorID:     ids.UserID,
		Now:         time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if row.Name != newName {
		t.Fatalf("expected rename to %s, got %+v", newName, row)
	}
	// Slug must stay frozen across updates.
	if row.Slug != "demo" {
		t.Fatalf("expected slug unchanged, got %q", row.Slug)
	}
}

func TestUpdateWorkspaceRejectsEmptyPatch(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	st := New(db)
	if _, err := st.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	ids := DefaultDevFixtureIDs()

	if _, err := st.UpdateWorkspace(ctx, UpdateWorkspaceInput{
		WorkspaceID: ids.WorkspaceID,
		ActorID:     ids.UserID,
		Now:         time.Now().UTC(),
	}); !errors.Is(err, ErrInvalidWorkspaceInput) {
		t.Fatalf("expected ErrInvalidWorkspaceInput, got %v", err)
	}
}

func TestArchiveWorkspaceSoftDeletes(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	st := New(db)
	if _, err := st.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	ids := DefaultDevFixtureIDs()

	if _, err := st.ArchiveWorkspace(ctx, ArchiveWorkspaceInput{
		WorkspaceID: ids.WorkspaceID,
		ActorID:     ids.UserID,
		Now:         time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	// Archived workspace must drop out of ListUserWorkspaces
	rows, err := st.ListUserWorkspaces(ctx, ids.UserID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected 0 active workspaces after archive, got %d: %+v", len(rows), rows)
	}
}

func TestArchiveWorkspaceRejectsMarketplaceDependents(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	st := New(db)
	if _, err := st.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	ids := DefaultDevFixtureIDs()
	sourceWorkspaceID := "00000000-0000-0000-0000-000000000202"
	capabilityID := "00000000-0000-0000-0000-000000000203"
	versionID := "00000000-0000-0000-0000-000000000204"

	stmts := []struct {
		sql  string
		args []any
	}{
		{`insert into workspaces(id, name, slug, created_at, updated_at) values ($1, 'Source Workspace', 'source-workspace', now(), now())`, []any{sourceWorkspaceID}},
		{`insert into workspace_members(id, workspace_id, user_id, role, created_at, updated_at) values (gen_random_uuid(), $1, $2, 'admin', now(), now())`, []any{sourceWorkspaceID, ids.UserID}},
		{`insert into capability(id, workspace_id, type, name, description, visibility, status, creator_id, created_at, updated_at) values ($1, $2, 'mcp', 'Shared Capability', '', 'public', 'active', $3, now(), now())`, []any{capabilityID, sourceWorkspaceID, ids.UserID}},
		{`insert into capability_version(id, capability_id, version, content, creator_id, created_at) values ($1, $2, '1.0.0', '{}'::jsonb, $3, now())`, []any{versionID, capabilityID, ids.UserID}},
		{`insert into agent_capabilities(id, agent_id, capability_id, capability_version_id, enabled, created_at, updated_at) values (gen_random_uuid(), $1, $2, $3, true, now(), now())`, []any{ids.BackendAgentID, capabilityID, versionID}},
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatalf("seed marketplace dependent: %v", err)
		}
	}

	_, err := st.ArchiveWorkspace(ctx, ArchiveWorkspaceInput{
		WorkspaceID: sourceWorkspaceID,
		ActorID:     ids.UserID,
		Now:         time.Now().UTC(),
	})
	if !errors.Is(err, ErrMarketplaceDependents) {
		t.Fatalf("expected ErrMarketplaceDependents, got %v", err)
	}
}

func TestGenerateAutoSlugShape(t *testing.T) {
	for _, prefix := range []string{"workspace", "project", "agent", "provider"} {
		got := generateAutoSlug(prefix)
		want := prefix + "-"
		if !strings.HasPrefix(got, want) {
			t.Errorf("generateAutoSlug(%q) = %q, expected prefix %q", prefix, got, want)
		}
		if len(got) != len(want)+12 {
			t.Errorf("generateAutoSlug(%q) = %q, expected suffix length 12 hex chars", prefix, got)
		}
		if strings.Trim(got[len(want):], "0123456789abcdef") != "" {
			t.Errorf("generateAutoSlug(%q) = %q, expected lowercase hex suffix", prefix, got)
		}
		// Two consecutive rolls almost surely differ; a collision means
		// the rand source is broken.
		other := generateAutoSlug(prefix)
		if got == other {
			t.Errorf("two consecutive generateAutoSlug(%q) collisions: %q", prefix, got)
		}
	}
}

func TestGetConversationRejectsUnknown(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)

	if _, err := store.GetConversation(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrUnknownConversation) {
		t.Fatalf("expected ErrUnknownConversation, got %v", err)
	}
}

func TestMapUserFacingReasonCoversCommonFailures(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		expect string
	}{
		{"empty", "", "Agent run failed. Please retry later or contact an administrator."},
		{"unknown", "unknown", "Agent run failed. Please retry later or contact an administrator."},
		{"secret disabled", "secret disabled or unavailable", "The required Secret is disabled or missing. Please verify it on the Secrets page."},
		{"model missing", "model disabled or missing", "The model bound to the Agent is disabled or does not exist. Please reselect it on the Agents page."},
		{"provider disabled", "provider disabled", "The model provider the Agent depends on is disabled. Please restore it or reselect on the Models page."},
		{"context length", "This model's maximum context length is 32k tokens", "The conversation exceeds the model's context length limit. Please start a new conversation or shorten the question and retry."},
		{"rate limit", "429 too many requests", "The model service is rate-limited. Please retry later."},
		// agent_daemon pairing expired: error text contains daemon context plus 401/timeout,
		// must hit the "pairing expired" branch instead of being misclassified as "model
		// Secret" or "model timeout".
		{"daemon ws 401", "parsar-daemon: connect: permanent error (re-pair the daemon): transport.Dial: ws upgrade rejected with 401 Unauthorized", "Agent container pairing expired. Please retry."},
		{"daemon dial-in timeout", "sandbox acquire: agent_daemon: sandbox acquire failed: wait for daemon dial-in (deviceID=495f7b1c…): context deadline exceeded", "Agent container pairing expired. Please retry."},
		{"acquire sandbox binding failed", "agent_daemon: acquireSandboxBinding failed: sandbox acquire: wait for daemon dial-in: context deadline exceeded", "Agent container pairing expired. Please retry."},
		// But a pure model 401 (without daemon context) still takes the original branch.
		{"unauthorized", "401 unauthorized", "Model service authentication failed. Please verify the Secret configuration."},
		{"forbidden", "403 forbidden", "The model service refused the request. Please verify account permissions."},
		{"timeout", "context deadline exceeded", "The model call timed out. Please retry later."},
		{"connection refused", "dial tcp 127.0.0.1:9099: connection refused", "Unable to connect to the model service. Please verify network and service address."},
		{"opencode exit", "opencode exec exit status 2", "Agent local execution failed. Please expand this run's error details for the cause."},
		{"generic", "some unexpected runner panic", "Agent run failed. Please expand this run's error details for the specific cause."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mapUserFacingReason(tc.input); got != tc.expect {
				t.Fatalf("mapUserFacingReason(%q) = %q, want %q", tc.input, got, tc.expect)
			}
		})
	}
}

func TestUserFacingReasonFromMetadataPrefersExplicit(t *testing.T) {
	meta := map[string]any{
		"failed_by":          "http_agent",
		"failure_reason":     "non-2xx",
		"user_facing_reason": "Agent run failed. Please retry later.",
	}
	if got := userFacingReasonFromMetadata(meta); got != "Agent run failed. Please retry later." {
		t.Fatalf("expected explicit reason wins, got %q", got)
	}

	derived := userFacingReasonFromMetadata(map[string]any{"failure_reason": "401 unauthorized"})
	if derived != "Model service authentication failed. Please verify the Secret configuration." {
		t.Fatalf("expected derived reason, got %q", derived)
	}

	if got := userFacingReasonFromMetadata(nil); got != "" {
		t.Fatalf("expected empty for nil metadata, got %q", got)
	}
}

func TestDisableEnableAgentRoundtripGuardsMentions(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store, auditIng := newAuditAwareStore(t, db)
	ids := DefaultDevFixtureIDs()
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}

	disabled, err := store.DisableAgent(ctx, ids.BackendAgentID)
	if err != nil {
		t.Fatal(err)
	}
	if disabled.Status != "disabled" || disabled.AgentID != ids.BackendAgentID {
		t.Fatalf("unexpected disable result: %+v", disabled)
	}
	flushAudit(t, auditIng)
	assertAuditEvent(t, db, ids.WorkspaceID, "agent.disabled", "agent", ids.BackendAgentID)

	// listing active agents should drop the disabled one
	agents, err := store.ListWorkspaceEnabledAgents(ctx, ids.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	for _, agent := range agents {
		if agent.AgentID == ids.BackendAgentID {
			t.Fatalf("expected disabled agent excluded from list, got %+v", agent)
		}
	}

	// mentioning a disabled agent must not produce queued runs;
	// surfaces ErrUnknownMention so the caller can render a UX hint.
	_, err = store.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{
		ConversationTitle: "Demo Group",
		SenderEmail:       "admin@example.com",
		Text:              "@backend-agent check the API",
		Mentions:          []string{"@backend-agent"},
	})
	if !errors.Is(err, ErrUnknownMention) {
		t.Fatalf("expected ErrUnknownMention when only disabled agent is mentioned, got %v", err)
	}

	// re-enable should restore matchability
	enabled, err := store.EnableAgent(ctx, ids.BackendAgentID)
	if err != nil {
		t.Fatal(err)
	}
	if enabled.Status != "active" {
		t.Fatalf("expected enable result active, got %+v", enabled)
	}
	flushAudit(t, auditIng)
	assertAuditEvent(t, db, ids.WorkspaceID, "agent.enabled", "agent", ids.BackendAgentID)

	created2, err := store.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{
		ConversationTitle: "Demo Group",
		SenderEmail:       "admin@example.com",
		Text:              "@backend-agent take another look",
		Mentions:          []string{"@backend-agent"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(created2.RunIDs) == 0 {
		t.Fatal("expected run created after re-enable")
	}
}

func TestDisableAgentRejectsUnknown(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)

	if _, err := store.DisableAgent(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrUnknownAgent) {
		t.Fatalf("expected ErrUnknownAgent, got %v", err)
	}
}

// assertWorkspaceAuditEvent verifies a workspace-scoped audit row exists.
func assertWorkspaceAuditEvent(t *testing.T, db *pgxpool.Pool, workspaceID string, eventType string, targetType string, targetID string) {
	t.Helper()
	var count int
	if err := db.QueryRow(context.Background(), `
		select count(*)
		from audit_records
		where workspace_id = $1::uuid
			and event_type = $2
			and target_type = $3
			and target_id = $4::uuid
	`, workspaceID, eventType, targetType, targetID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected one workspace audit event %s/%s/%s, got %d", eventType, targetType, targetID, count)
	}
}

// assertWorkspaceAuditMetadata reads metadata->>key off the latest
// workspace-scoped audit row matching the event_type.
func assertWorkspaceAuditMetadata(t *testing.T, db *pgxpool.Pool, workspaceID string, eventType string, key string, want string) {
	t.Helper()
	var got string
	if err := db.QueryRow(context.Background(), `
		select payload->>$3
		from audit_records
		where workspace_id = $1::uuid
			and event_type = $2
		order by occurred_at desc, id desc
		limit 1
	`, workspaceID, eventType, key).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("expected workspace audit payload %s=%q for %s, got %q", key, want, eventType, got)
	}
}

func TestAddWorkspaceMemberWritesAuditLog(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store, auditIng := newAuditAwareStore(t, db)
	ids := DefaultDevFixtureIDs()
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}

	result, err := store.AddWorkspaceMember(ctx, AddWorkspaceMemberInput{
		WorkspaceID: ids.WorkspaceID,
		Email:       "audit-test@example.com",
		Name:        "Audit Test",
		Role:        "member",
		Now:         time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}

	flushAudit(t, auditIng)
	assertWorkspaceAuditEvent(t, db, ids.WorkspaceID, "workspace_member.added", "workspace_member", result.Member.ID)
	assertWorkspaceAuditMetadata(t, db, ids.WorkspaceID, "workspace_member.added", "source", "dev_member_write")
	assertWorkspaceAuditMetadata(t, db, ids.WorkspaceID, "workspace_member.added", "user_email", "audit-test@example.com")
	assertWorkspaceAuditMetadata(t, db, ids.WorkspaceID, "workspace_member.added", "role", "member")
}

// TestAddWorkspaceMemberLowercasesInviteEmail proves the invite path
// stores the email in the same canonical form the login handler
// queries with. Symmetry between the two sides is what lets a caller
// invited as "Bob@Company.com" then POST /auth/login {email:"bob@company.com"}
// and hit their own row instead of a 401.
//
// Also asserts the auth_identities.subject bound to the temp password
// is the folded email, since /auth/login joins users.email against
// auth_identities.subject.
func TestAddWorkspaceMemberLowercasesInviteEmail(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	ids := DefaultDevFixtureIDs()
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := store.AddWorkspaceMember(ctx, AddWorkspaceMemberInput{
		WorkspaceID: ids.WorkspaceID,
		Email:       "  Bob@Company.COM  ",
		Name:        "Bob",
		Role:        "member",
		Now:         time.Now().UTC(),
	}); err != nil {
		t.Fatalf("AddWorkspaceMember: %v", err)
	}

	q := sqlc.New(db)
	uid, err := q.GetActiveUserIDByEmail(ctx, "bob@company.com")
	if err != nil {
		t.Fatalf("stored user email should be lowercase; GetActiveUserIDByEmail: %v", err)
	}
	if uid == "" {
		t.Fatalf("expected a user id for lowercased email, got empty")
	}
	// The mixed-case form must miss — proves normalization is
	// applied and reads should also fold.
	if _, err := q.GetActiveUserIDByEmail(ctx, "Bob@Company.COM"); err == nil {
		t.Fatalf("mixed-case lookup should miss the folded row")
	}
}

// TestMarkAgentRunRunningStateTransitionsAndConversationGuard pins the
// boundary cases: only queued → running succeeds; running/completed →
// running is rejected; a wrong conversationID on a real runID is
// rejected (guards against ID stitching across conversations); unknown
// runID returns ErrUnknownAgentRun.
func TestMarkAgentRunRunningStateTransitionsAndConversationGuard(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	ids := DefaultDevFixtureIDs()
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	sent, err := store.SendUserMessageToConversation(ctx, SendUserMessageToConversationInput{
		ConversationID:    ids.ConversationID,
		UserID:            ids.UserID,
		Content:           "@product-agent run stream store test",
		MentionedAgentIDs: []string{ids.ProductAgentID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sent.RunIDs) == 0 {
		t.Fatal("expected at least one agent run from the mention")
	}
	runID := sent.RunIDs[0]

	// queued → running: happy path.
	started, err := store.MarkAgentRunRunning(ctx, runID, ids.ConversationID)
	if err != nil {
		t.Fatalf("first MarkAgentRunRunning err = %v", err)
	}
	if started.RunID != runID || started.Status != "running" || started.StartedAt.IsZero() {
		t.Fatalf("first MarkAgentRunRunning result = %+v, want running with started_at", started)
	}

	if _, err := store.MarkAgentRunRunning(ctx, runID, ids.ConversationID); !errors.Is(err, ErrAgentRunNotStartable) {
		t.Fatalf("second MarkAgentRunRunning err = %v, want ErrAgentRunNotStartable", err)
	}

	if _, err := db.Exec(ctx, `update agent_runs set status = 'completed', finished_at = now() where id = $1`, runID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkAgentRunRunning(ctx, runID, ids.ConversationID); !errors.Is(err, ErrAgentRunNotStartable) {
		t.Fatalf("completed MarkAgentRunRunning err = %v, want ErrAgentRunNotStartable", err)
	}

	// Fresh queued run + foreign conversation id: row-mismatch is the
	// only reason for rejection.
	sent2, err := store.SendUserMessageToConversation(ctx, SendUserMessageToConversationInput{
		ConversationID:    ids.ConversationID,
		UserID:            ids.UserID,
		Content:           "@product-agent second run",
		MentionedAgentIDs: []string{ids.ProductAgentID},
	})
	if err != nil {
		t.Fatal(err)
	}
	bogusConversation := "00000000-0000-0000-0000-0000000abcde"
	if _, err := store.MarkAgentRunRunning(ctx, sent2.RunIDs[0], bogusConversation); !errors.Is(err, ErrAgentRunNotStartable) {
		t.Fatalf("wrong-conversation MarkAgentRunRunning err = %v, want ErrAgentRunNotStartable", err)
	}

	if _, err := store.MarkAgentRunRunning(ctx, "00000000-0000-0000-0000-0000feedf00d", ids.ConversationID); !errors.Is(err, ErrUnknownAgentRun) {
		t.Fatalf("unknown run MarkAgentRunRunning err = %v, want ErrUnknownAgentRun", err)
	}
}

// TestSendAssistantMessageFromRunPersistsAssistantMessageAndCompletes
// asserts the store helper writes an assistant message bound to the
// conversation, marks the run completed with finished_at, and stamps
// the source-specific audit event.
func TestSendAssistantMessageFromRunPersistsAssistantMessageAndCompletes(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	ids := DefaultDevFixtureIDs()
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	sent, err := store.SendUserMessageToConversation(ctx, SendUserMessageToConversationInput{
		ConversationID:    ids.ConversationID,
		UserID:            ids.UserID,
		Content:           "@product-agent assistant persist",
		MentionedAgentIDs: []string{ids.ProductAgentID},
	})
	if err != nil {
		t.Fatal(err)
	}
	runID := sent.RunIDs[0]
	if _, err := store.MarkAgentRunRunning(ctx, runID, ids.ConversationID); err != nil {
		t.Fatal(err)
	}

	out, err := store.SendAssistantMessageFromRun(ctx, SendAssistantMessageFromRunInput{
		RunID:   runID,
		Source:  "conversation_stream",
		Content: "已完成",
		Usage:   UsageInput{Provider: "fake", Model: "parsar-test-stream"},
	})
	if err != nil {
		t.Fatalf("SendAssistantMessageFromRun err = %v", err)
	}
	if out.Status != "completed" || out.FinishedAt.IsZero() || out.MessageID == "" {
		t.Fatalf("SendAssistantMessageFromRun result = %+v, want completed+finished+message", out)
	}

	var status string
	var finishedAt *time.Time
	if err := db.QueryRow(ctx, `select status, finished_at from agent_runs where id = $1`, runID).Scan(&status, &finishedAt); err != nil {
		t.Fatal(err)
	}
	if status != "completed" || finishedAt == nil {
		t.Fatalf("run row status/finished_at = %s/%v, want completed/non-nil", status, finishedAt)
	}

	var assistantCount int
	if err := db.QueryRow(ctx, `select count(*) from messages where conversation_id = $1 and sender_type = 'agent' and metadata->>'run_id' = $2`, ids.ConversationID, runID).Scan(&assistantCount); err != nil {
		t.Fatal(err)
	}
	if assistantCount != 1 {
		t.Fatalf("assistant messages bound to run = %d, want 1", assistantCount)
	}
}

// TestSendUserMessageImplicitPrimaryAgentDispatchesWithoutMention: when
// a conversation has a bound primary_agent (metadata.primary_agent_id),
// a bare message body implicitly targets that agent and creates exactly
// one agent_run; explicit @-mention and explicit MentionedAgentIDs still
// win.
func TestSendUserMessageImplicitPrimaryAgentDispatchesWithoutMention(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	ids := DefaultDevFixtureIDs()
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}

	conv, err := store.CreateWorkspaceConversation(ctx, CreateWorkspaceConversationInput{
		WorkspaceID:    ids.WorkspaceID,
		Title:          "1v1 implicit primary",
		PrimaryAgentID: ids.ProductAgentID,
	})
	if err != nil {
		t.Fatal(err)
	}

	sent, err := store.SendUserMessageToConversation(ctx, SendUserMessageToConversationInput{
		ConversationID: conv.ID,
		UserID:         ids.UserID,
		Content:        "你好，不用 @ 也应该派 run",
	})
	if err != nil {
		t.Fatalf("SendUserMessageToConversation err = %v", err)
	}
	if len(sent.RunIDs) != 1 {
		t.Fatalf("implicit primary should dispatch exactly 1 run, got %d (ids=%v)", len(sent.RunIDs), sent.RunIDs)
	}

	var paID string
	if err := db.QueryRow(ctx, `select agent_id::text from agent_runs where id = $1`, sent.RunIDs[0]).Scan(&paID); err != nil {
		t.Fatal(err)
	}
	if paID != ids.ProductAgentID {
		t.Fatalf("dispatched agent_id = %s, want %s (the bound primary)", paID, ids.ProductAgentID)
	}

	// Explicit @-mention does not double-dispatch when the user mentions
	// the same agent as the bound primary.
	sent2, err := store.SendUserMessageToConversation(ctx, SendUserMessageToConversationInput{
		ConversationID: conv.ID,
		UserID:         ids.UserID,
		Content:        "@product-agent explicit mention should not dupe",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sent2.RunIDs) != 1 {
		t.Fatalf("explicit @primary + bound primary should still be 1 run, got %d", len(sent2.RunIDs))
	}

	// Unbound conversation + bare message → no dispatch.
	convNoPrimary, err := store.CreateWorkspaceConversation(ctx, CreateWorkspaceConversationInput{
		WorkspaceID: ids.WorkspaceID,
		Title:       "no primary",
	})
	if err != nil {
		t.Fatal(err)
	}
	sent3, err := store.SendUserMessageToConversation(ctx, SendUserMessageToConversationInput{
		ConversationID: convNoPrimary.ID,
		UserID:         ids.UserID,
		Content:        "bare message in unbound convo",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sent3.RunIDs) != 0 {
		t.Fatalf("unbound conv + bare message should dispatch 0 runs, got %d", len(sent3.RunIDs))
	}
}

// recordingStreamingDispatcher captures every Start call so tests can
// assert agent_daemon runs (and only those) are auto-started by the
// post-commit hook. Fire-and-forget per the interface.
type recordingStreamingDispatcher struct {
	mu    sync.Mutex
	calls []StreamingDispatchInput
}

func (r *recordingStreamingDispatcher) Start(_ context.Context, in StreamingDispatchInput) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, in)
}

func (r *recordingStreamingDispatcher) Snapshot() []StreamingDispatchInput {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]StreamingDispatchInput, len(r.calls))
	copy(out, r.calls)
	return out
}

// TestSendUserMessageAgentDaemonAgentAutoStartsStreaming: prompts
// addressed to an agent_daemon agent create an agent_run row, return
// the run_id to the caller, and enqueue the async streaming dispatcher.
func TestSendUserMessageAgentDaemonAgentAutoStartsStreaming(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	st := New(db)
	ids := DefaultDevFixtureIDs()
	if _, err := st.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}

	created, err := st.CreateAgent(ctx, CreateAgentInput{
		WorkspaceID:   ids.WorkspaceID,
		Name:          "Daemon Test Agent",
		Slug:          "daemon-test-agent",
		ConnectorType: "agent_daemon",
		CreatedBy:     ids.UserID,
	})
	if err != nil {
		t.Fatalf("CreateAgent(agent_daemon): %v", err)
	}
	// agentConfigJSON must NOT write config.runtime for agent_daemon.
	if _, ok := created.Agent.Config["runtime"]; ok {
		t.Fatalf("agent_daemon agents must not persist config.runtime, got %#v", created.Agent.Config)
	}

	conv, err := st.CreateWorkspaceConversation(ctx, CreateWorkspaceConversationInput{
		WorkspaceID:    ids.WorkspaceID,
		Title:          "daemon dispatch probe",
		PrimaryAgentID: created.Agent.ID,
	})
	if err != nil {
		t.Fatal(err)
	}

	streamRec := &recordingStreamingDispatcher{}
	st.SetStreamingDispatcher(streamRec)
	sent, err := st.SendUserMessageToConversation(ctx, SendUserMessageToConversationInput{
		ConversationID: conv.ID,
		UserID:         ids.UserID,
		Content:        "ping daemon",
	})
	if err != nil {
		t.Fatalf("SendUserMessageToConversation: %v", err)
	}
	if len(sent.RunIDs) != 1 {
		t.Fatalf("agent_daemon agent should still get 1 queued run, got %d (ids=%v)", len(sent.RunIDs), sent.RunIDs)
	}
	// Without the server-side auto-start hook the run sits at queued
	// forever and the daemon never sees the prompt.
	if got := streamRec.Snapshot(); len(got) != 1 {
		t.Fatalf("agent_daemon must hit streaming dispatcher exactly once, got %d calls: %#v", len(got), got)
	} else if got[0].RunID != sent.RunIDs[0] || got[0].ConversationID != conv.ID || got[0].ConnectorType != "agent_daemon" {
		t.Fatalf("streaming dispatcher input mismatch: got %#v, want RunID=%s ConversationID=%s ConnectorType=agent_daemon",
			got[0], sent.RunIDs[0], conv.ID)
	}
}

// TestCreateAgentDaemonRejectsRuntimeValue: agent_daemon agents have no
// server-side runtime; passing a non-empty runtime returns
// ErrInvalidInput, and the no-runtime success path persists a
// config without the "runtime" key.
func TestCreateAgentDaemonRejectsRuntimeValue(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	st := New(db)
	ids := DefaultDevFixtureIDs()
	if _, err := st.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}

	for _, badRuntime := range []string{"local", "sandbox"} {
		_, err := st.CreateAgent(ctx, CreateAgentInput{
			WorkspaceID:   ids.WorkspaceID,
			Name:          "daemon-bad-runtime-" + badRuntime,
			Slug:          "daemon-bad-runtime-" + badRuntime,
			ConnectorType: "agent_daemon",
			Runtime:       badRuntime,
			CreatedBy:     ids.UserID,
		})
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("CreateAgent(agent_daemon, runtime=%q) = %v, want ErrInvalidInput", badRuntime, err)
		}
	}

	created, err := st.CreateAgent(ctx, CreateAgentInput{
		WorkspaceID:   ids.WorkspaceID,
		Name:          "daemon-ok",
		Slug:          "daemon-ok",
		ConnectorType: "agent_daemon",
		AgentConfig: map[string]any{
			"device_id":   "00000000-0000-0000-0000-00000000d001",
			"daemon_mode": "local",
			"agent_kind":  "claude_code",
			"ignored":     "not-persisted",
		},
		CreatedBy: ids.UserID,
	})
	if err != nil {
		t.Fatalf("CreateAgent(agent_daemon, runtime=\"\"): %v", err)
	}
	if _, ok := created.Agent.Config["runtime"]; ok {
		t.Fatalf("agent_daemon config must omit runtime key, got %#v", created.Agent.Config)
	}
	if got := created.Agent.Config["device_id"]; got != "00000000-0000-0000-0000-00000000d001" {
		t.Fatalf("agent.config device_id mismatch: got %#v", got)
	}
	if got := created.Agent.Config["daemon_mode"]; got != "local" {
		t.Fatalf("agent.config daemon_mode mismatch: got %#v", got)
	}
	if _, ok := created.Agent.Config["ignored"]; ok {
		t.Fatalf("agent.config must not persist unknown daemon key: %#v", created.Agent.Config)
	}

	// Persisted JSON is the ground truth — read back to make sure
	// agentConfigJSON did not silently default to "sandbox".
	var rawConfig string
	if err := db.QueryRow(ctx, `select config::text from agents where id = $1::uuid`, created.Agent.ID).Scan(&rawConfig); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rawConfig, `"runtime"`) {
		t.Fatalf("agents.config must not contain runtime for agent_daemon, got %s", rawConfig)
	}
	var rawPAConfig string
	if err := db.QueryRow(ctx, `select config::text from agents where id = $1::uuid`, created.Agent.ID).Scan(&rawPAConfig); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rawPAConfig, `"device_id": "00000000-0000-0000-0000-00000000d001"`) || strings.Contains(rawPAConfig, `"ignored"`) {
		t.Fatalf("agents.config did not persist only daemon keys, got %s", rawPAConfig)
	}
}

// TestAgentSummaryFromRowIgnoresLegacyRuntime: historical agent_daemon
// rows may still have config["runtime"] persisted; the list/detail
// surface strips it on read because the daemon owns runtime selection.
// (Chosen over a destructive data migration.)
func TestAgentSummaryFromRowIgnoresLegacyRuntime(t *testing.T) {
	cfg := []byte(`{"runtime":"sandbox","capabilities":["foo"]}`)
	summary := agentSummaryFromRow("aid", "wsid", "n", "s", "d", "agent_daemon", "active", cfg, pgtype.Timestamptz{}, pgtype.Timestamptz{})
	if _, ok := summary.Config["runtime"]; ok {
		t.Fatalf("agent_daemon row must strip legacy config.runtime, got %#v", summary.Config)
	}
	if caps, _ := summary.Config["capabilities"].([]any); len(caps) != 1 {
		t.Fatalf("capabilities must survive the strip, got %#v", summary.Config["capabilities"])
	}

	// HTTP keeps the runtime field; only agent_daemon strips it.
	httpAgent := agentSummaryFromRow("aid2", "wsid", "n", "s", "d", "http", "active", cfg, pgtype.Timestamptz{}, pgtype.Timestamptz{})
	if got, _ := httpAgent.Config["runtime"].(string); got != "sandbox" {
		t.Fatalf("http row must preserve config.runtime, got %#v", httpAgent.Config)
	}
}

// TestCreateInboundIMMessageInitiatorUserIDShortCircuitsLookup covers
// the ADR-004 credential-form re-enqueue path: when the caller already
// knows the Parsar user_id (because it was stashed on the slot at
// inbound time), passing it via InitiatorUserID must populate
// agent_runs.requested_by_id directly — without going through
// auth_identities.subject. Otherwise the re-fired run lands with an
// empty initiator and any MCP needing per-user credentials trips the
// "conversation_initiator_id is empty" runtime error.
func TestCreateInboundIMMessageInitiatorUserIDShortCircuitsLookup(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	st := New(db)
	ids := DefaultDevFixtureIDs()
	if _, err := st.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}

	created, err := st.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{
		Text: "rerun after credential bind",
		// Deliberately NO ExternalUserID / SenderEmail — the lookup
		// fallbacks must NOT fire; InitiatorUserID alone wins.
		InitiatorUserID:   ids.UserID,
		Source:            "gateway",
		Gateway:           "feishu",
		ExternalChatID:    "oc_rerun",
		ExternalMessageID: "om_rerun_qkey",
		TargetAgentID:     ids.BackendAgentID,
		SourceAppID:       "cli_rerun_bot",
		ConversationForm:  "dm",
	})
	if err != nil {
		t.Fatalf("CreateInboundIMMessage: %v", err)
	}
	if len(created.RunIDs) != 1 {
		t.Fatalf("expected one run, got %+v", created)
	}

	var requestedByType, requestedByID string
	if err := db.QueryRow(ctx, `
		select requested_by_type, requested_by_id::text
		from agent_runs
		where id = $1::uuid
	`, created.RunIDs[0]).Scan(&requestedByType, &requestedByID); err != nil {
		t.Fatal(err)
	}
	if requestedByType != "user" {
		t.Errorf("requested_by_type = %q, want \"user\"", requestedByType)
	}
	if requestedByID != ids.UserID {
		t.Errorf("requested_by_id = %q, want fixture user %q", requestedByID, ids.UserID)
	}
}

// TestResolveAgentNameForConversationReadsPrimaryAgentMetadata pins the
// "what Agent is this conversation talking to" lookup against the
// actual storage shape — conversations.metadata.primary_agent_id (a
// agents.id) joined through to agents.name — instead of the
// non-existent conversations.selected_agent_id the query previously
// referenced. The 42703 SQLSTATE the broken version surfaced in prod
// took down the per-card header titles on every credential-form /
// permission-result / NoticeCard patch path; this test makes sure the
// regression can't return silently.
func TestResolveAgentNameForConversationReadsPrimaryAgentMetadata(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	st := New(db)
	ids := DefaultDevFixtureIDs()
	if _, err := st.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}

	// CreateInboundIMMessage with a TargetAgentID stamps
	// metadata.primary_agent_id on the new conversation row — that's
	// the production write path we're trying to mirror.
	created, err := st.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{
		Text:              "hello",
		Source:            "gateway",
		Gateway:           "feishu",
		ExternalUserID:    "ou_feishu_admin",
		ExternalChatID:    "oc_resolve_name",
		ExternalMessageID: "om_resolve_name",
		TargetAgentID:     ids.BackendAgentID,
		SourceAppID:       "cli_resolve_name",
		ConversationForm:  "dm",
	})
	if err != nil {
		t.Fatalf("CreateInboundIMMessage: %v", err)
	}

	var conversationID string
	if err := db.QueryRow(ctx, `
		select conversation_id::text from messages where id = $1::uuid
	`, created.MessageID).Scan(&conversationID); err != nil {
		t.Fatal(err)
	}

	name, err := st.ResolveAgentNameForConversation(ctx, conversationID)
	if err != nil {
		t.Fatalf("ResolveAgentNameForConversation: %v", err)
	}
	if name != "Backend Agent" {
		t.Errorf("agent name = %q, want fixture backend agent name Backend Agent", name)
	}

	// Unknown conversation must collapse to ('', nil) — the LEFT JOINs
	// only protect that when the conversation row itself exists; pgx
	// surfaces ErrNoRows otherwise, which callers handle by falling
	// back to the brand title.
	unknown, err := st.ResolveAgentNameForConversation(ctx, "00000000-0000-0000-0000-000000000fff")
	if err == nil && unknown != "" {
		t.Errorf("unknown conversation: got name=%q err=nil, want ('' + ErrNoRows-equivalent)", unknown)
	}
}
