package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestCoreAgentContract(t *testing.T) {
	st := New(openTestDB(t))
	ctx := context.Background()
	ids := mustSeedDevFixture(t, ctx, st)
	base := CreateAgentInput{WorkspaceID: ids.WorkspaceID, Name: "Core Agent", ConnectorType: "agents_api", CreatedBy: ids.UserID, SystemPrompt: "Be concise", AgentConfig: map[string]any{"model": "test-model"}}
	created, err := st.CreateAgent(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	if created.Agent.Config["model"] != "test-model" || created.Agent.Config["system_prompt"] != "Be concise" {
		t.Fatalf("lost Core configuration: %+v", created.Agent.Config)
	}
	for _, kind := range []string{"agent_daemon", "http", "opencode_local"} {
		in := base
		in.ConnectorType = kind
		if _, err := st.CreateAgent(ctx, in); !errors.Is(err, ErrInvalidConnectorType) {
			t.Fatalf("accepted legacy connector %s: %v", kind, err)
		}
	}
	for _, config := range []map[string]any{nil, {"model": ""}, {"model": "test-model", "device_id": "device"}, {"model": "test-model", "credential_bindings": "invalid"}} {
		in := base
		in.AgentConfig = config
		if _, err := st.CreateAgent(ctx, in); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("accepted unsupported config: %v", err)
		}
	}
	for _, in := range []CreateAgentInput{
		{Runtime: "sandbox"}, {DefaultModelID: ids.BackendAgentID}, {Capabilities: []string{"shell"}},
	} {
		in.WorkspaceID, in.Name, in.ConnectorType, in.CreatedBy, in.AgentConfig = base.WorkspaceID, base.Name, base.ConnectorType, base.CreatedBy, base.AgentConfig
		if _, err := st.CreateAgent(ctx, in); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("accepted execution settings: %v", err)
		}
	}
	model := map[string]any{"model": "next-model"}
	updated, _, err := st.UpdateAgent(ctx, UpdateAgentInput{AgentID: created.Agent.ID, ActorID: ids.UserID, ConfigSet: true, Config: model})
	if err != nil || updated.Config["system_prompt"] != "Be concise" || updated.Config["model"] != "next-model" {
		t.Fatalf("partial Core edit: %+v %v", updated, err)
	}
}

func TestCoreBindingsSurviveReplayAndSerializeCancellation(t *testing.T) {
	db := openTestDB(t)
	st := New(db)
	ctx := context.Background()
	ids := mustSeedDevFixture(t, ctx, st)
	conv, err := st.CreateWorkspaceConversation(ctx, CreateWorkspaceConversationInput{WorkspaceID: ids.WorkspaceID, PrimaryAgentID: ids.BackendAgentID, Title: "Core recovery"})
	if err != nil {
		t.Fatal(err)
	}
	send := func(text string) string {
		t.Helper()
		result, err := st.SendUserMessageToConversation(ctx, SendUserMessageToConversationInput{ConversationID: conv.ID, UserID: ids.UserID, Content: text})
		if err != nil || len(result.RunIDs) != 1 {
			t.Fatalf("send: %+v %v", result, err)
		}
		return result.RunIDs[0]
	}
	first, second := send("First"), send("Second")
	request := json.RawMessage(`{"agent":{"model":"test-model"}}`)
	session, err := st.EnsureCoreSession(ctx, first, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.BindCoreSession(ctx, session.ID, "session-core"); err != nil {
		t.Fatal(err)
	}
	repeated, err := st.EnsureCoreSession(ctx, second, json.RawMessage(`{"agent":{"model":"edited-model"}}`))
	if err != nil || repeated.ID != session.ID || repeated.SessionID != "session-core" || string(repeated.Request) != string(session.Request) {
		t.Fatalf("conversation snapshot changed: %+v %v", repeated, err)
	}
	if err := st.BindCoreSession(ctx, session.ID, "another-session"); err == nil {
		t.Fatal("overwrote committed session binding")
	}
	if err := st.EnsureCoreRun(ctx, first, session.ID, json.RawMessage(`{"text":"first"}`)); err != nil {
		t.Fatal(err)
	}
	if err := st.EnsureCoreRun(ctx, first, session.ID, json.RawMessage(`{"text":"edited"}`)); err != nil {
		t.Fatal(err)
	}
	if err := st.SetCoreRunBaseline(ctx, first, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.SetCoreRunBaseline(ctx, first, "later-turn"); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkCoreRunAttempted(ctx, first); err != nil {
		t.Fatal(err)
	}
	binding, err := st.GetCoreRun(ctx, first)
	if err != nil || string(binding.Input) != `{"text": "first"}` || !binding.BaselineSet || binding.PreviousTurnID != "" || !binding.Attempted {
		t.Fatalf("lost frozen run snapshot: %+v %v", binding, err)
	}
	acquiredBefore := db.Stat().AcquiredConns()
	lease, release, err := st.ClaimCoreRun(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	if db.Stat().AcquiredConns() != acquiredBefore {
		t.Fatal("execution lease consumed the product query pool")
	}
	if _, _, err := st.ClaimCoreRun(ctx, second); !errors.Is(err, ErrCoreExecutionClaimed) {
		t.Fatalf("concurrent lane claimed: %v", err)
	}
	if _, err := st.CancelAgentRun(ctx, first, "test"); err != nil {
		t.Fatal(err)
	}
	release()
	if lease.Err() == nil {
		t.Fatal("released observer context remains live")
	}
	if _, _, err := st.ClaimCoreRun(ctx, second); !errors.Is(err, ErrCoreExecutionClaimed) {
		t.Fatalf("unsettled cancellation did not fence successor: %v", err)
	}
	recoverable, err := st.ListRecoverableCoreRuns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, run := range recoverable {
		if run.RunID == first {
			found = true
		}
	}
	if !found {
		t.Fatal("cancelled uncertain submission missing from recovery")
	}
	if err := st.SettleCoreRun(ctx, first); err != nil {
		t.Fatal(err)
	}
	_, releaseNext, err := st.ClaimCoreRun(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	releaseNext()
}

func TestCoreConfigReplacementClearsRemovedFields(t *testing.T) {
	st := New(openTestDB(t))
	ctx := context.Background()
	ids := mustSeedDevFixture(t, ctx, st)
	a, err := st.CreateAgent(ctx, CreateAgentInput{WorkspaceID: ids.WorkspaceID, Name: "configuration replacement", CreatedBy: ids.UserID, ConnectorType: "agents_api", SystemPrompt: "Review changes", AgentConfig: map[string]any{"model": "test-model", "tools": []any{map[string]any{"type": "web_search"}}, "reasoning": map[string]any{"effort": "high"}}})
	if err != nil {
		t.Fatal(err)
	}
	updated, _, err := st.UpdateAgent(ctx, UpdateAgentInput{AgentID: a.Agent.ID, ActorID: ids.UserID, ConfigSet: true, Config: map[string]any{"model": "test-model"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := updated.Config["tools"]; ok {
		t.Fatal("removed tools survived save")
	}
	if _, ok := updated.Config["reasoning"]; ok {
		t.Fatal("removed reasoning survived save")
	}
	if updated.Config["system_prompt"] != "Review changes" {
		t.Fatal("SP lost")
	}
}

func TestCoreRetiredAgentRecoveryAndTerminalUsage(t *testing.T) {
	for _, terminal := range []string{"failed", "cancelled", "completed"} {
		t.Run(terminal, func(t *testing.T) {
			db := openTestDB(t)
			st := New(db)
			ctx := context.Background()
			ids := mustSeedDevFixture(t, ctx, st)
			result, err := st.SendUserMessageToConversation(ctx, SendUserMessageToConversationInput{ConversationID: ids.ConversationID, UserID: ids.UserID, Content: "Run", MentionedAgentIDs: []string{ids.BackendAgentID}})
			if err != nil || len(result.RunIDs) != 1 {
				t.Fatalf("admission: %+v %v", result, err)
			}
			runID := result.RunIDs[0]
			binding, err := st.EnsureCoreSession(ctx, runID, json.RawMessage(`{"agent":{"model":"frozen-model","instructions":"Frozen"}}`))
			if err != nil {
				t.Fatal(err)
			}
			if err := st.EnsureCoreRun(ctx, runID, binding.ID, json.RawMessage(`{"events":[]}`)); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(ctx, `update agents set status='disabled',deleted_at=now(),config='{}' where id=$1`, ids.BackendAgentID); err != nil {
				t.Fatal(err)
			}
			if _, err := st.GetAgentRunInvocation(ctx, runID); err != nil {
				t.Fatalf("retired Agent blocks observation: %v", err)
			}
			if _, err := st.GetCoreSession(ctx, runID); err != nil {
				t.Fatal(err)
			}
			usage := UsageInput{Provider: "agents_api", Model: "frozen-model", InputTokens: 12, OutputTokens: 3, Raw: map[string]any{"measured": true}}
			if terminal == "cancelled" {
				if _, err := st.CancelAgentRun(ctx, runID, "test"); err != nil {
					t.Fatal(err)
				}
			}
			if terminal == "failed" {
				if err := st.FailAgentRun(ctx, FailAgentRunInput{RunID: runID, Source: "agents_api", Reason: "test"}); err != nil {
					t.Fatal(err)
				}
			}
			for i := 0; i < 2; i++ {
				if err := st.RecordCoreUsage(ctx, runID, usage); err != nil {
					t.Fatal(err)
				}
			}
			if terminal == "completed" {
				if _, err := st.CompleteAgentRun(ctx, CompleteAgentRunInput{RunID: runID, Source: "agents_api", Content: "Done", Usage: usage}); err != nil {
					t.Fatalf("retired Agent blocks completion: %v", err)
				}
			}
			var count, input int
			if err := db.QueryRow(ctx, `select count(*),coalesce(sum(input_tokens),0) from usage_logs where agent_run_id=$1`, runID).Scan(&count, &input); err != nil {
				t.Fatal(err)
			}
			if count != 1 || input != 12 {
				t.Fatalf("usage lost or duplicated: %d %d", count, input)
			}
		})
	}
}

func TestLegacyAgentsCannotAdmitNewExecution(t *testing.T) {
	db := openTestDB(t)
	st := New(db)
	ctx := context.Background()
	ids := mustSeedDevFixture(t, ctx, st)
	sent, err := st.SendUserMessageToConversation(ctx, SendUserMessageToConversationInput{ConversationID: ids.ConversationID, UserID: ids.UserID, Content: "first", MentionedAgentIDs: []string{ids.BackendAgentID}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.FailAgentRun(ctx, FailAgentRunInput{RunID: sent.RunIDs[0], Reason: "test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `update agents set connector_type='agent_daemon' where id=$1`, ids.BackendAgentID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SendUserMessageToConversation(ctx, SendUserMessageToConversationInput{ConversationID: ids.ConversationID, UserID: ids.UserID, Content: "legacy", MentionedAgentIDs: []string{ids.BackendAgentID}}); !errors.Is(err, ErrInvalidConnectorType) {
		t.Fatalf("web admitted legacy: %v", err)
	}
	if _, err := st.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{TargetAgentID: ids.BackendAgentID, ExternalChatID: "legacy", Text: "legacy", InitiatorUserID: ids.UserID}); !errors.Is(err, ErrInvalidConnectorType) {
		t.Fatalf("IM admitted legacy: %v", err)
	}
	if _, err := st.RetryAgentRun(ctx, RetryAgentRunInput{RunID: sent.RunIDs[0], UserID: ids.UserID}); !errors.Is(err, ErrInvalidConnectorType) {
		t.Fatalf("retry admitted legacy: %v", err)
	}
	if _, err := st.CreateScheduledTask(ctx, CreateScheduledTaskInput{AgentID: ids.BackendAgentID, Name: "legacy", Prompt: "legacy", CronExpr: "0 * * * *", Timezone: "UTC", CreatedBy: ids.UserID}); !errors.Is(err, ErrInvalidConnectorType) {
		t.Fatalf("schedule admitted legacy: %v", err)
	}
}

func TestDeletedConversationRetainsCoreCleanup(t *testing.T) {
	for _, initialStatus := range []string{"queued", "running", "failed", "completed"} {
		t.Run(initialStatus, func(t *testing.T) {
			db := openTestDB(t)
			st := New(db)
			ctx := context.Background()
			ids := mustSeedDevFixture(t, ctx, st)
			sent, err := st.SendUserMessageToConversation(ctx, SendUserMessageToConversationInput{ConversationID: ids.ConversationID, UserID: ids.UserID, Content: "cleanup", MentionedAgentIDs: []string{ids.BackendAgentID}})
			if err != nil {
				t.Fatal(err)
			}
			runID := sent.RunIDs[0]
			session, err := st.EnsureCoreSession(ctx, runID, json.RawMessage(`{"agent":{"model":"test"}}`))
			if err != nil {
				t.Fatal(err)
			}
			if err := st.EnsureCoreRun(ctx, runID, session.ID, json.RawMessage(`{}`)); err != nil {
				t.Fatal(err)
			}
			if err := st.MarkCoreRunAttempted(ctx, runID); err != nil {
				t.Fatal(err)
			}
			if err := st.MarkCoreRunSubmitted(ctx, runID); err != nil {
				t.Fatal(err)
			}
			if err := st.RecordAgentRunEvent(ctx, RecordAgentRunEventInput{RunID: runID, EventKind: "message.delta", Payload: map[string]any{"delta": "partial"}}); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(ctx, `update agent_runs set status=$2 where id=$1`, runID, initialStatus); err != nil {
				t.Fatal(err)
			}
			if err := st.SoftDeleteConversation(ctx, ids.ConversationID); err != nil {
				t.Fatal(err)
			}
			if err := st.SoftDeleteConversation(ctx, ids.ConversationID); !errors.Is(err, ErrUnknownConversation) {
				t.Fatalf("second deletion: %v", err)
			}
			if _, err := st.GetAgentRun(ctx, runID); !errors.Is(err, ErrUnknownAgentRun) {
				t.Fatalf("public run visible: %v", err)
			}
			if _, err := st.ListAgentRunEvents(ctx, runID, 0); !errors.Is(err, ErrUnknownAgentRun) {
				t.Fatalf("public events visible: %v", err)
			}
			status, err := st.GetCoreExecutionStatus(ctx, runID)
			expected := initialStatus
			if initialStatus == "queued" || initialStatus == "running" {
				expected = "cancelled"
			}
			if err != nil || status != expected {
				t.Fatalf("lost cancellation: %s %v", status, err)
			}
			if _, err := st.GetAgentRunInvocation(ctx, runID); err != nil {
				t.Fatalf("cannot recover cleanup: %v", err)
			}
			events, err := st.ListCoreExecutionEvents(ctx, runID, 0)
			if err != nil || len(events) != 1 {
				t.Fatalf("cannot restore: %+v %v", events, err)
			}
			pending, err := st.ListRecoverableCoreRuns(ctx)
			if err != nil || len(pending) != 1 || pending[0].RunID != runID {
				t.Fatalf("not recoverable: %+v %v", pending, err)
			}
			// A cancelled execution must acknowledge a terminal observation without
			// publishing a contradictory terminal event, even after deletion.
			if expected == "cancelled" {
				if err := st.RecordAgentRunEvent(ctx, RecordAgentRunEventInput{RunID: runID, EventKind: "run.failed"}); err != nil {
					t.Fatal(err)
				}
			}
			if err := st.RecordCoreUsage(ctx, runID, UsageInput{InputTokens: 12, OutputTokens: 3}); err != nil {
				t.Fatal(err)
			}
			if err := st.SettleCoreRun(ctx, runID); err != nil {
				t.Fatal(err)
			}
			pending, err = st.ListRecoverableCoreRuns(ctx)
			if err != nil || len(pending) != 0 {
				t.Fatalf("cleanup not settled: %+v %v", pending, err)
			}
		})
	}
}
