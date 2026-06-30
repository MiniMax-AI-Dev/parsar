package inflight

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// fakeNeutralChannel is a minimal channel.Channel for the neutral
// outbound-delivery tests. It records every RenderProgress / RenderTerminal /
// Send / Edit call so a test can assert the driver took the right path without
// any live transport. Send returns a monotonically increasing message id so
// the slot capture is observable.
type fakeNeutralChannel struct {
	platform channel.Platform

	progressRenders int
	terminalRenders int
	lastTerminal    channel.TerminalResult
	lastProgress    channel.ProgressState

	permissionRenders int
	choiceRenders     int
	credentialRenders int
	lastPermission    channel.PermissionRequest
	lastChoice        channel.ChoiceForm
	lastCredential    channel.CredentialForm

	sends []neutralSendCall
	edits []neutralEditCall

	nextID int
}

type neutralSendCall struct {
	Target channel.ReplyTarget
	Card   channel.Card
	ID     string
}

type neutralEditCall struct {
	Target channel.ReplyTarget
	Ref    gateway.MessageRef
	Card   channel.Card
}

func (f *fakeNeutralChannel) Platform() channel.Platform { return f.platform }
func (f *fakeNeutralChannel) Capabilities() channel.Capabilities {
	return channel.Capabilities{Edit: true, BlockStreaming: true}
}
func (f *fakeNeutralChannel) Verify(*http.Request, []byte) ([]byte, string, error) {
	return nil, "", nil
}
func (f *fakeNeutralChannel) Normalize([]byte) (gateway.InboundEvent, error) {
	return gateway.InboundEvent{}, nil
}
func (f *fakeNeutralChannel) Reply(context.Context, channel.ReplyTarget, string) error { return nil }

func (f *fakeNeutralChannel) RenderProgress(_ context.Context, _ channel.ReplyTarget, state channel.ProgressState) (channel.Card, error) {
	f.progressRenders++
	f.lastProgress = state
	return channel.Card{MIME: "test/progress", Payload: []byte(`{"kind":"progress"}`)}, nil
}

func (f *fakeNeutralChannel) RenderTerminal(_ context.Context, _ channel.ReplyTarget, result channel.TerminalResult) (channel.Card, error) {
	f.terminalRenders++
	f.lastTerminal = result
	return channel.Card{MIME: "test/terminal", Payload: []byte(`{"kind":"terminal"}`)}, nil
}

func (f *fakeNeutralChannel) Stream() channel.StreamMode { return channel.StreamPatches }

func (f *fakeNeutralChannel) RenderPermission(_ context.Context, _ channel.ReplyTarget, req channel.PermissionRequest) (channel.Card, error) {
	f.permissionRenders++
	f.lastPermission = req
	return channel.Card{MIME: "test/permission", Payload: []byte(`{"kind":"permission"}`)}, nil
}

func (f *fakeNeutralChannel) RenderChoiceForm(_ context.Context, _ channel.ReplyTarget, form channel.ChoiceForm) (channel.Card, error) {
	f.choiceRenders++
	f.lastChoice = form
	return channel.Card{MIME: "test/choice", Payload: []byte(`{"kind":"choice"}`)}, nil
}

func (f *fakeNeutralChannel) RenderCredentialForm(_ context.Context, _ channel.ReplyTarget, form channel.CredentialForm) (channel.Card, error) {
	f.credentialRenders++
	f.lastCredential = form
	return channel.Card{MIME: "test/credential", Payload: []byte(`{"kind":"credential"}`)}, nil
}

func (f *fakeNeutralChannel) Edit(_ context.Context, target channel.ReplyTarget, ref gateway.MessageRef, card channel.Card) error {
	f.edits = append(f.edits, neutralEditCall{Target: target, Ref: ref, Card: card})
	return nil
}

func (f *fakeNeutralChannel) Send(_ context.Context, target channel.ReplyTarget, card channel.Card) (gateway.MessageRef, error) {
	f.nextID++
	id := "ts-" + strconv.Itoa(f.nextID)
	f.sends = append(f.sends, neutralSendCall{Target: target, Card: card, ID: id})
	return gateway.MessageRef{ID: id}, nil
}

func (f *fakeNeutralChannel) HandleAction(context.Context, []byte) (channel.ActionResult, error) {
	return channel.ActionResult{}, nil
}
func (f *fakeNeutralChannel) AgentPromptHint() string                 { return "" }
func (f *fakeNeutralChannel) Credentials() channel.CredentialResolver { return nil }

// newNeutralWorker builds a worker with one registered neutral channel for the
// given platform, sharing the fakeStore. Secrets is a no-op decrypter — the
// neutral path never touches the worker's Feishu credential machinery.
func newNeutralWorker(t *testing.T, fs *fakeStore, ch channel.Channel) *Worker {
	t.Helper()
	w, err := NewWorker(Options{
		Store:   fs,
		Secrets: fakeDecrypter{},
		Channels: map[channel.Platform]channel.Channel{
			ch.Platform(): ch,
		},
	})
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	return w
}

// assertNoFeishuOnlySurfaces fails if the neutral path touched any Feishu-only
// store method. The whole point of the isolated path is that Slack delivery
// never reaches the permission / credential-form / reaction / queue machinery.
func assertNoFeishuOnlySurfaces(t *testing.T, fs *fakeStore) {
	t.Helper()
	if len(fs.permissionUpserts) != 0 {
		t.Errorf("permission upserts = %d, want 0 (neutral path must not send permission cards)", len(fs.permissionUpserts))
	}
	if len(fs.promptForUserChoiceUpserts) != 0 {
		t.Errorf("prompt_for_user_choice upserts = %d, want 0", len(fs.promptForUserChoiceUpserts))
	}
	if len(fs.pendingFormsWritten) != 0 {
		t.Errorf("credential form writes = %d, want 0", len(fs.pendingFormsWritten))
	}
	if len(fs.reactionClears) != 0 {
		t.Errorf("reaction clears = %d, want 0 (neutral path has no typing reaction)", len(fs.reactionClears))
	}
	if len(fs.systemNotices) != 0 {
		t.Errorf("system notices = %d, want 0", len(fs.systemNotices))
	}
}

// TestNeutralInflight_MidRunFirstSend covers a Slack conversation with no
// inflight slot and one tool.call event: the driver renders a progress card,
// SENDs it (no Edit), and upserts the slot with the captured message id and an
// empty ExpectedOldRunID.
func TestNeutralInflight_MidRunFirstSend(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	ch := &fakeNeutralChannel{platform: channel.PlatformSlack}
	w := newNeutralWorker(t, fs, ch)

	c := store.FeishuInflightConversation{
		ConversationID:   "conv-slack-1",
		WorkspaceID:      "ws-1",
		Platform:         "slack",
		ExternalChatID:   "C123",
		ExternalThreadID: "1700000000.0001",
		AgentRunID:       "run-1",
		RunStatus:        "running",
		RunStartedAt:     time.Now().Add(-2 * time.Second).UTC(),
		MaxEventSequence: 1,
	}
	fs.inflightConvs = []store.FeishuInflightConversation{c}
	fs.inflightEvents["run-1"] = []store.AgentRunEvent{{
		Sequence:  1,
		EventKind: "tool.call",
		Payload:   map[string]any{"name": "Bash", "stage": "before", "args": map[string]any{"command": "ls"}},
	}}

	if err := w.handleInflightConversation(context.Background(), c); err != nil {
		t.Fatalf("handleInflightConversation: %v", err)
	}

	if ch.progressRenders != 1 {
		t.Errorf("progress renders = %d, want 1", ch.progressRenders)
	}
	if len(ch.sends) != 1 {
		t.Fatalf("sends = %d, want 1 (first send)", len(ch.sends))
	}
	if len(ch.edits) != 0 {
		t.Errorf("edits = %d, want 0 on first send", len(ch.edits))
	}
	if ch.sends[0].Target.ExternalChatID != "C123" || ch.sends[0].Target.ExternalThreadID != "1700000000.0001" {
		t.Errorf("send target = %+v, want chat C123 / thread set", ch.sends[0].Target)
	}
	if len(fs.inflightUpserts) != 1 {
		t.Fatalf("upserts = %d, want 1", len(fs.inflightUpserts))
	}
	up := fs.inflightUpserts[0]
	if up.ExpectedOldRunID != "" {
		t.Errorf("first-send ExpectedOldRunID = %q, want empty", up.ExpectedOldRunID)
	}
	if up.Slot.ExternalMsgID != "ts-1" {
		t.Errorf("slot ExternalMsgID = %q, want ts-1 (captured send id)", up.Slot.ExternalMsgID)
	}
	if up.Slot.SeqEmitted != 1 {
		t.Errorf("slot SeqEmitted = %d, want 1", up.Slot.SeqEmitted)
	}
	assertNoFeishuOnlySurfaces(t, fs)
}

// TestNeutralInflight_MidRunSubsequentEdit covers a conversation that already
// owns a working slot and gets a fresh event: the driver renders progress and
// EDITs the pinned message (no new Send), upserting with the prior run id as
// the optimistic-lock guard.
func TestNeutralInflight_MidRunSubsequentEdit(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	ch := &fakeNeutralChannel{platform: channel.PlatformSlack}
	w := newNeutralWorker(t, fs, ch)

	metadata := map[string]any{
		"gateway_inflight": map[string]any{
			"working": map[string]any{
				"external_msg_id":  "ts-existing",
				"external_chat_id": "C123",
				"agent_run_id":     "run-2",
				"seq_emitted":      float64(1),
			},
		},
	}
	c := store.FeishuInflightConversation{
		ConversationID:       "conv-slack-2",
		WorkspaceID:          "ws-1",
		Platform:             "slack",
		ExternalChatID:       "C123",
		AgentRunID:           "run-2",
		RunStatus:            "running",
		RunStartedAt:         time.Now().Add(-4 * time.Second).UTC(),
		MaxEventSequence:     2,
		ConversationMetadata: metadata,
	}
	fs.inflightConvs = []store.FeishuInflightConversation{c}
	fs.inflightEvents["run-2"] = []store.AgentRunEvent{
		{Sequence: 1, EventKind: "tool.call", Payload: map[string]any{"name": "Read", "stage": "before", "args": map[string]any{"file_path": "a.go"}}},
		{Sequence: 2, EventKind: "tool.call", Payload: map[string]any{"name": "Edit", "stage": "before", "args": map[string]any{"file_path": "a.go"}}},
	}

	if err := w.handleInflightConversation(context.Background(), c); err != nil {
		t.Fatalf("handleInflightConversation: %v", err)
	}

	if len(ch.edits) != 1 {
		t.Fatalf("edits = %d, want 1 (subsequent patch)", len(ch.edits))
	}
	if len(ch.sends) != 0 {
		t.Errorf("sends = %d, want 0 on subsequent tick", len(ch.sends))
	}
	if ch.edits[0].Ref.ID != "ts-existing" {
		t.Errorf("edit ref id = %q, want ts-existing", ch.edits[0].Ref.ID)
	}
	if len(fs.inflightUpserts) != 1 {
		t.Fatalf("upserts = %d, want 1", len(fs.inflightUpserts))
	}
	up := fs.inflightUpserts[0]
	if up.ExpectedOldRunID != "run-2" {
		t.Errorf("patch ExpectedOldRunID = %q, want run-2", up.ExpectedOldRunID)
	}
	if up.Slot.SeqEmitted != 2 {
		t.Errorf("patch SeqEmitted = %d, want 2", up.Slot.SeqEmitted)
	}
	assertNoFeishuOnlySurfaces(t, fs)
}

// TestNeutralInflight_TerminalWithSlotEdits covers a completed run whose
// working card is already on screen: the driver renders the Done card and EDITs
// the pinned message, then stamps the messages-side delivery marker, the per-run
// fingerprint, and clears the slot.
func TestNeutralInflight_TerminalWithSlotEdits(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	ch := &fakeNeutralChannel{platform: channel.PlatformSlack}
	w := newNeutralWorker(t, fs, ch)

	metadata := map[string]any{
		"gateway_inflight": map[string]any{
			"working": map[string]any{
				"external_msg_id":  "ts-working",
				"external_chat_id": "C123",
				"agent_run_id":     "run-3",
				"seq_emitted":      float64(1),
			},
		},
	}
	c := store.FeishuInflightConversation{
		ConversationID:       "conv-slack-3",
		WorkspaceID:          "ws-1",
		Platform:             "slack",
		ExternalChatID:       "C123",
		AgentRunID:           "run-3",
		RunStatus:            "completed",
		OutputMessageID:      "msg-out-3",
		RunStartedAt:         time.Now().Add(-6 * time.Second).UTC(),
		RunFinishedAt:        time.Now().UTC(),
		MaxEventSequence:     1,
		ConversationMetadata: metadata,
	}
	fs.inflightConvs = []store.FeishuInflightConversation{c}

	if err := w.handleInflightConversation(context.Background(), c); err != nil {
		t.Fatalf("handleInflightConversation: %v", err)
	}

	if ch.terminalRenders != 1 || !ch.lastTerminal.Success {
		t.Errorf("terminal renders = %d success = %v, want 1 / true", ch.terminalRenders, ch.lastTerminal.Success)
	}
	if len(ch.edits) != 1 {
		t.Fatalf("edits = %d, want 1 (terminal patch)", len(ch.edits))
	}
	if ch.edits[0].Ref.ID != "ts-working" {
		t.Errorf("terminal edit ref = %q, want ts-working", ch.edits[0].Ref.ID)
	}
	if len(ch.sends) != 0 {
		t.Errorf("sends = %d, want 0 (slot present)", len(ch.sends))
	}
	if len(fs.delivered) != 1 || fs.delivered[0].MessageID != "msg-out-3" {
		t.Errorf("delivered = %+v, want one mark for msg-out-3", fs.delivered)
	}
	if len(fs.terminalDeliveredMarks) != 1 {
		t.Errorf("terminal fingerprint marks = %d, want 1", len(fs.terminalDeliveredMarks))
	}
	if len(fs.inflightClears) != 1 {
		t.Errorf("slot clears = %d, want 1", len(fs.inflightClears))
	}
	assertNoFeishuOnlySurfaces(t, fs)
}

// TestNeutralInflight_TerminalWithoutSlotSends covers a short run that finished
// before any working card landed: no slot, so the driver SENDs a fresh Done
// card and still stamps the fingerprint.
func TestNeutralInflight_TerminalWithoutSlotSends(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	ch := &fakeNeutralChannel{platform: channel.PlatformSlack}
	w := newNeutralWorker(t, fs, ch)

	c := store.FeishuInflightConversation{
		ConversationID:   "conv-slack-4",
		WorkspaceID:      "ws-1",
		Platform:         "slack",
		ExternalChatID:   "C123",
		AgentRunID:       "run-4",
		RunStatus:        "completed",
		OutputMessageID:  "msg-out-4",
		RunStartedAt:     time.Now().Add(-1 * time.Second).UTC(),
		RunFinishedAt:    time.Now().UTC(),
		MaxEventSequence: 0,
	}
	fs.inflightConvs = []store.FeishuInflightConversation{c}

	if err := w.handleInflightConversation(context.Background(), c); err != nil {
		t.Fatalf("handleInflightConversation: %v", err)
	}

	if len(ch.sends) != 1 {
		t.Fatalf("sends = %d, want 1 (own the terminal send)", len(ch.sends))
	}
	if len(ch.edits) != 0 {
		t.Errorf("edits = %d, want 0 (no slot)", len(ch.edits))
	}
	if len(fs.terminalDeliveredMarks) != 1 {
		t.Errorf("terminal fingerprint marks = %d, want 1", len(fs.terminalDeliveredMarks))
	}
	assertNoFeishuOnlySurfaces(t, fs)
}

// TestNeutralInflight_FailedRendersErrorCard pins the error path: a failed run
// renders the Error card (Success=false) carrying the folded error message.
func TestNeutralInflight_FailedRendersErrorCard(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	ch := &fakeNeutralChannel{platform: channel.PlatformSlack}
	w := newNeutralWorker(t, fs, ch)

	c := store.FeishuInflightConversation{
		ConversationID:   "conv-slack-5",
		WorkspaceID:      "ws-1",
		Platform:         "slack",
		ExternalChatID:   "C123",
		AgentRunID:       "run-5",
		RunStatus:        "failed",
		RunStartedAt:     time.Now().Add(-2 * time.Second).UTC(),
		RunFinishedAt:    time.Now().UTC(),
		MaxEventSequence: 1,
	}
	fs.inflightConvs = []store.FeishuInflightConversation{c}
	fs.inflightEvents["run-5"] = []store.AgentRunEvent{{
		Sequence:  1,
		EventKind: "run.failed",
		Payload:   map[string]any{"error": "boom upstream"},
	}}

	if err := w.handleInflightConversation(context.Background(), c); err != nil {
		t.Fatalf("handleInflightConversation: %v", err)
	}

	if ch.terminalRenders != 1 || ch.lastTerminal.Success {
		t.Errorf("terminal renders = %d success = %v, want 1 / false", ch.terminalRenders, ch.lastTerminal.Success)
	}
	if len(ch.sends) != 1 {
		t.Errorf("sends = %d, want 1 (no slot, owns send)", len(ch.sends))
	}
	if len(fs.terminalDeliveredMarks) != 1 {
		t.Errorf("terminal fingerprint marks = %d, want 1", len(fs.terminalDeliveredMarks))
	}
	assertNoFeishuOnlySurfaces(t, fs)
}

// TestNeutralInflight_FeishuRowSkipsNeutralPath guards the platform branch:
// a row with platform "feishu" (or empty) must NOT be routed to the neutral
// channel even when one is registered. We assert the registered neutral channel
// stays untouched; the Feishu path itself errors out on the empty source_app_id
// guard, which is fine — we only care that the neutral channel was bypassed.
func TestNeutralInflight_FeishuRowSkipsNeutralPath(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	ch := &fakeNeutralChannel{platform: channel.PlatformSlack}
	w := newNeutralWorker(t, fs, ch)

	c := store.FeishuInflightConversation{
		ConversationID:   "conv-feishu-1",
		WorkspaceID:      "ws-1",
		Platform:         "feishu",
		ExternalChatID:   "oc_x",
		AgentRunID:       "run-f",
		RunStatus:        "running",
		MaxEventSequence: 1,
	}
	// Empty SourceAppID makes the Feishu path return its guard error; we
	// only assert the neutral channel was not used.
	_ = w.handleInflightConversation(context.Background(), c)

	if ch.progressRenders != 0 || ch.terminalRenders != 0 || len(ch.sends) != 0 || len(ch.edits) != 0 {
		t.Errorf("neutral channel touched for a feishu row: progress=%d terminal=%d sends=%d edits=%d",
			ch.progressRenders, ch.terminalRenders, len(ch.sends), len(ch.edits))
	}
}

// TestNeutralInflight_MidRunEmitsPermissionCard covers a running Slack
// conversation whose tick observes a permission.asked event: the driver sends
// the working card AND, independently, an Allow/Deny permission card, then
// upserts the permission slot pinning the request id with an empty
// ExpectedOldRequestID (first send). The two cards live in separate slots.
func TestNeutralInflight_MidRunEmitsPermissionCard(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	ch := &fakeNeutralChannel{platform: channel.PlatformSlack}
	w := newNeutralWorker(t, fs, ch)

	c := store.FeishuInflightConversation{
		ConversationID:   "conv-slack-perm",
		WorkspaceID:      "ws-1",
		Platform:         "slack",
		ExternalChatID:   "C123",
		ExternalThreadID: "1700000000.0001",
		AgentRunID:       "run-perm",
		RunStatus:        "running",
		RunStartedAt:     time.Now().Add(-2 * time.Second).UTC(),
		MaxEventSequence: 2,
	}
	fs.inflightConvs = []store.FeishuInflightConversation{c}
	fs.inflightEvents["run-perm"] = []store.AgentRunEvent{
		{Sequence: 1, EventKind: "tool.call", Payload: map[string]any{"name": "Bash", "stage": "before", "args": map[string]any{"command": "rm -rf /tmp/cache"}}},
		{Sequence: 2, EventKind: "permission.asked", Payload: map[string]any{
			"request_id": "perm-xyz-1",
			"action":     "Bash",
			"detail":     "rm -rf /tmp/cache",
		}},
	}

	if err := w.handleInflightConversation(context.Background(), c); err != nil {
		t.Fatalf("handleInflightConversation: %v", err)
	}

	if ch.permissionRenders != 1 {
		t.Errorf("permission renders = %d, want 1", ch.permissionRenders)
	}
	if ch.lastPermission.RequestID != "perm-xyz-1" || ch.lastPermission.ToolName != "Bash" {
		t.Errorf("rendered permission = %+v, want request perm-xyz-1 / tool Bash", ch.lastPermission)
	}
	// Two sends: working card first, then the permission card.
	if len(ch.sends) != 2 {
		t.Fatalf("sends = %d, want 2 (working + permission)", len(ch.sends))
	}
	if len(fs.permissionUpserts) != 1 {
		t.Fatalf("permissionUpserts = %d, want 1", len(fs.permissionUpserts))
	}
	up := fs.permissionUpserts[0]
	if up.Slot.PermissionRequestID != "perm-xyz-1" {
		t.Errorf("slot PermissionRequestID = %q, want perm-xyz-1", up.Slot.PermissionRequestID)
	}
	if up.Slot.ExternalMsgID != "ts-2" {
		t.Errorf("slot ExternalMsgID = %q, want ts-2 (second send id)", up.Slot.ExternalMsgID)
	}
	if up.ExpectedOldRequestID != "" {
		t.Errorf("first permission-send ExpectedOldRequestID = %q, want empty", up.ExpectedOldRequestID)
	}
	// The working slot is still upserted independently.
	if len(fs.inflightUpserts) != 1 {
		t.Errorf("working upserts = %d, want 1 (independent of permission)", len(fs.inflightUpserts))
	}
}

// TestNeutralInflight_PermissionCardIdempotent verifies a slot already pinning
// the same request id short-circuits: no second render, no second send, no
// upsert. One card per pending request, not one per tick.
func TestNeutralInflight_PermissionCardIdempotent(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	ch := &fakeNeutralChannel{platform: channel.PlatformSlack}
	w := newNeutralWorker(t, fs, ch)

	metadata := map[string]any{
		"gateway_inflight": map[string]any{
			"working": map[string]any{
				"external_msg_id":  "ts-working",
				"external_chat_id": "C123",
				"agent_run_id":     "run-perm2",
				"seq_emitted":      float64(1),
			},
			"permission": map[string]any{
				"external_msg_id":       "ts-perm",
				"external_chat_id":      "C123",
				"agent_run_id":          "run-perm2",
				"permission_request_id": "perm-existing",
			},
		},
	}
	c := store.FeishuInflightConversation{
		ConversationID:       "conv-slack-perm2",
		WorkspaceID:          "ws-1",
		Platform:             "slack",
		ExternalChatID:       "C123",
		AgentRunID:           "run-perm2",
		RunStatus:            "running",
		RunStartedAt:         time.Now().Add(-3 * time.Second).UTC(),
		MaxEventSequence:     2,
		ConversationMetadata: metadata,
	}
	fs.inflightConvs = []store.FeishuInflightConversation{c}
	fs.inflightEvents["run-perm2"] = []store.AgentRunEvent{
		{Sequence: 2, EventKind: "permission.asked", Payload: map[string]any{
			"request_id": "perm-existing",
			"action":     "Bash",
			"detail":     "ls",
		}},
	}

	if err := w.handleInflightConversation(context.Background(), c); err != nil {
		t.Fatalf("handleInflightConversation: %v", err)
	}

	if ch.permissionRenders != 0 {
		t.Errorf("permission renders = %d, want 0 (already pinned)", ch.permissionRenders)
	}
	if len(fs.permissionUpserts) != 0 {
		t.Errorf("permissionUpserts = %d, want 0 (idempotent)", len(fs.permissionUpserts))
	}
}

// TestNeutralInflight_MidRunEmitsChoiceCard covers a running Slack conversation
// whose tick observes a prompt_for_user_choice.asked event: the driver sends the
// working card AND, independently, the choice form, then upserts the choice slot
// pinning the request id with an empty ExpectedOldRequestID (first send). The two
// cards live in separate slots, exactly like the permission twin.
func TestNeutralInflight_MidRunEmitsChoiceCard(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	ch := &fakeNeutralChannel{platform: channel.PlatformSlack}
	w := newNeutralWorker(t, fs, ch)

	c := store.FeishuInflightConversation{
		ConversationID:   "conv-slack-choice",
		WorkspaceID:      "ws-1",
		Platform:         "slack",
		ExternalChatID:   "C123",
		ExternalThreadID: "1700000000.0001",
		AgentRunID:       "run-choice",
		RunStatus:        "running",
		RunStartedAt:     time.Now().Add(-2 * time.Second).UTC(),
		MaxEventSequence: 2,
	}
	fs.inflightConvs = []store.FeishuInflightConversation{c}
	fs.inflightEvents["run-choice"] = []store.AgentRunEvent{
		{Sequence: 1, EventKind: "tool.call", Payload: map[string]any{"name": "Bash", "stage": "before", "args": map[string]any{"command": "echo hi"}}},
		{Sequence: 2, EventKind: "prompt_for_user_choice.asked", Payload: map[string]any{
			"request_id": "ask-xyz-1",
			"questions": []any{
				map[string]any{
					"header":       "Pick a branch",
					"question":     "Which branch should I target?",
					"multi_select": false,
					"options":      []any{map[string]any{"label": "main"}, map[string]any{"label": "develop"}},
				},
			},
		}},
	}

	if err := w.handleInflightConversation(context.Background(), c); err != nil {
		t.Fatalf("handleInflightConversation: %v", err)
	}

	if ch.choiceRenders != 1 {
		t.Errorf("choice renders = %d, want 1", ch.choiceRenders)
	}
	if ch.lastChoice.RequestID != "ask-xyz-1" {
		t.Errorf("rendered choice request id = %q, want ask-xyz-1", ch.lastChoice.RequestID)
	}
	if len(ch.lastChoice.Questions) != 1 || ch.lastChoice.Questions[0].Header != "Pick a branch" {
		t.Errorf("rendered choice questions = %+v, want one mapped question", ch.lastChoice.Questions)
	}
	if len(ch.lastChoice.Questions) == 1 && len(ch.lastChoice.Questions[0].Options) != 2 {
		t.Errorf("rendered choice options = %v, want [main develop] labels", ch.lastChoice.Questions[0].Options)
	}
	// Two sends: working card first, then the choice card.
	if len(ch.sends) != 2 {
		t.Fatalf("sends = %d, want 2 (working + choice)", len(ch.sends))
	}
	if len(fs.promptForUserChoiceUpserts) != 1 {
		t.Fatalf("promptForUserChoiceUpserts = %d, want 1", len(fs.promptForUserChoiceUpserts))
	}
	up := fs.promptForUserChoiceUpserts[0]
	if up.Slot.RequestID != "ask-xyz-1" {
		t.Errorf("slot RequestID = %q, want ask-xyz-1", up.Slot.RequestID)
	}
	if up.Slot.ExternalMsgID != "ts-2" {
		t.Errorf("slot ExternalMsgID = %q, want ts-2 (second send id)", up.Slot.ExternalMsgID)
	}
	if up.ExpectedOldRequestID != "" {
		t.Errorf("first choice-send ExpectedOldRequestID = %q, want empty", up.ExpectedOldRequestID)
	}
	// The working slot is still upserted independently.
	if len(fs.inflightUpserts) != 1 {
		t.Errorf("working upserts = %d, want 1 (independent of choice)", len(fs.inflightUpserts))
	}
}

// TestNeutralInflight_TerminalEmitsCredentialForm covers a completed Slack run
// that finished missing a capability credential with a recoverable inbound: the
// terminal branch ships the qkey-bearing credential form INSTEAD of the Done
// card, stashes a durable slot, stamps the slot's external_msg_id on first
// landing, and still closes the run (messages-side marker + per-run fingerprint
// + working-slot clear) so the claim CTE drops it.
func TestNeutralInflight_TerminalEmitsCredentialForm(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	ch := &fakeNeutralChannel{platform: channel.PlatformSlack}
	w := newNeutralWorker(t, fs, ch)

	c := store.FeishuInflightConversation{
		ConversationID:   "conv-slack-cred",
		WorkspaceID:      "ws-1",
		Platform:         "slack",
		ExternalChatID:   "C123",
		AgentRunID:       "run-cred",
		RunStatus:        "completed",
		OutputMessageID:  "msg-out-cred",
		RunStartedAt:     time.Now().Add(-3 * time.Second).UTC(),
		RunFinishedAt:    time.Now().UTC(),
		MaxEventSequence: 0,
	}
	fs.inflightConvs = []store.FeishuInflightConversation{c}
	fs.missingNotices = map[string][]store.CapabilityCredentialMissingNotice{
		"conv-slack-cred|run-cred": {
			{CapabilityID: "cap-github", CapabilityName: "GitHub", CredentialKind: "github_pat"},
		},
	}
	fs.inboundUserMsg = map[string]store.InboundUserMessageForRun{
		"conv-slack-cred": {
			RawQuery:      "list my open PRs",
			TargetAgentID: "agt-1",
			SenderUserID:  "u_bob",
			SenderOpenID:  "ou_bob",
		},
	}

	if err := w.handleInflightConversation(context.Background(), c); err != nil {
		t.Fatalf("handleInflightConversation: %v", err)
	}

	if ch.credentialRenders != 1 {
		t.Errorf("credential renders = %d, want 1", ch.credentialRenders)
	}
	if ch.terminalRenders != 0 {
		t.Errorf("terminal (Done) renders = %d, want 0 (form replaces the Done card)", ch.terminalRenders)
	}
	if ch.lastCredential.Qkey == "" {
		t.Error("rendered credential form Qkey empty, want the minted qkey")
	}
	if len(ch.lastCredential.Fields) != 1 || ch.lastCredential.Fields[0].Kind != "github_pat" {
		t.Errorf("rendered credential fields = %+v, want one github_pat field", ch.lastCredential.Fields)
	}
	// No working slot → the form is the terminal SEND, not an edit.
	if len(ch.sends) != 1 {
		t.Fatalf("sends = %d, want 1 (own the terminal form send)", len(ch.sends))
	}
	if len(ch.edits) != 0 {
		t.Errorf("edits = %d, want 0 (no slot)", len(ch.edits))
	}
	// A durable slot was stashed and its external_msg_id stamped on landing.
	if len(fs.pendingFormsWritten) != 1 {
		t.Fatalf("pendingFormsWritten = %d, want 1", len(fs.pendingFormsWritten))
	}
	if got := fs.pendingFormsWritten[0].Slot.RawQuery; got != "list my open PRs" {
		t.Errorf("stashed raw_query = %q, want verbatim user input", got)
	}
	if len(fs.pendingFormMsgIDStamps) != 1 || fs.pendingFormMsgIDStamps[0].ExternalMsgID != "ts-1" {
		t.Errorf("pendingForm msg-id stamps = %+v, want one stamp of ts-1", fs.pendingFormMsgIDStamps)
	}
	// The run is still closed out like any terminal delivery.
	if len(fs.delivered) != 1 || fs.delivered[0].MessageID != "msg-out-cred" {
		t.Errorf("delivered = %+v, want one mark for msg-out-cred", fs.delivered)
	}
	if len(fs.terminalDeliveredMarks) != 1 {
		t.Errorf("terminal fingerprint marks = %d, want 1", len(fs.terminalDeliveredMarks))
	}
	if len(fs.inflightClears) != 1 {
		t.Errorf("slot clears = %d, want 1", len(fs.inflightClears))
	}
}
