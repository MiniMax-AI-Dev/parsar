package inflight

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// fakeStore implements the inflight-driver slice of Storer as
// in-memory state shared by every package-local test file
// (inflight_driver_test.go, retry_test.go, permission_card_test.go,
// reaction_undo_test.go, done_card_data_test.go).
//
// The P1 outbound-poll surface (pending queue / claim / retry / dead-
// letter) was removed in the driver-only refactor; what remains is
// the inflight-driver state plus the credential + reaction stubs the
// driver also calls.
type fakeStore struct {
	mu sync.Mutex

	agents    map[string]store.FeishuAgentRoute // app_id -> route
	agentErr  error
	secrets   map[string]store.SecretPayload // secret_id -> payload
	secretErr error

	// delivered captures every MarkGatewayOutboundDelivered the driver
	// fires after a successful terminal patch. Tests that assert the
	// gateway_delivered_at stamp was issued inspect this slice.
	delivered []store.MarkGatewayOutboundDeliveredInput
	// markErrSequence lets a test simulate transient MarkDelivered
	// failures: each entry is the error for the Nth call (nil means
	// success). After the slice is exhausted, MarkDelivered succeeds.
	markErrSequence []error
	markErrIdx      int

	// Inflight driver state. Tests that exercise the inflight loop
	// populate `inflightConvs` (one row per conversation to surface to
	// the driver) and `inflightEvents[runID]` (the events
	// ListAgentRunEventsAfterSeq should return). The driver records
	// every Upsert / Clear it issued into the slices below so tests
	// can assert exactly what happened.
	//
	// inflightClaimCalls records every ClaimActiveFeishuInflightConversations
	// invocation. Tests that exercise the claim path assert on this
	// list to check claimed_by / stale_before were threaded through;
	// legacy tests just leave inflightConvs populated and the claim
	// path returns the same rows.
	inflightConvs      []store.FeishuInflightConversation
	inflightEvents     map[string][]store.AgentRunEvent
	inflightCutoffs    []time.Time
	inflightClaimCalls []store.ClaimActiveFeishuInflightConversationsInput
	inflightUpserts                []store.UpsertConversationInflightWorkingCardInput
	inflightClears                 []fakeInflightClear
	permissionUpserts              []store.UpsertConversationInflightPermissionCardInput
	promptForUserChoiceUpserts     []store.UpsertConversationInflightPromptForUserChoiceCardInput
	cardsByConv                    map[string]store.ConversationInflightCards
	cardsByPermReq                 map[string]store.ConversationInflightCards
	cardsByPromptForUserChoiceReq  map[string]store.ConversationInflightCards
	stalePermissions               []store.ConversationInflightCards
	stalePromptForUserChoice       []store.ConversationInflightCards
	staleCutoffs                   []time.Time

	// terminalDeliveredMarks records every MarkConversationInflightTerminalDelivered
	// call so tests can assert the per-run idempotency fingerprint
	// fired exactly once at the end of the terminal-card path. Pairs
	// up with `delivered` (which only records the messages-side
	// MarkGatewayOutboundDelivered call) — the fingerprint MUST fire
	// even when the messages-side marker is skipped because the run
	// has no output_message_id.
	terminalDeliveredMarks []fakeTerminalDeliveredMark

	// Typing-reaction undo state. Tests that exercise the terminal
	// patch -> DeleteReaction flow seed reactionsByConv keyed by
	// conversation_id; reactionClears captures every
	// ClearFeishuInboundReaction call so the test can assert the
	// metadata cleanup actually fired.
	reactionsByConv map[string]store.FeishuInboundReactionRow
	// reactionsByAgentRun mirrors reactionsByConv but keyed by
	// agent_run_id, so tests that exercise the per-run undo path
	// (resolveReactionRowForRun) can pin the lookup to a specific run.
	// Default empty: FindFeishuInboundReactionByAgentRun returns
	// ErrUnknownMessage and the caller falls back to the conversation
	// lookup, preserving pre-existing test behaviour.
	reactionsByAgentRun map[string]store.FeishuInboundReactionRow
	reactionClears      []string

	// doneCardData feeds LoadDoneCardRunData. Tests that exercise the
	// DoneCard footer (cost / context tokens / model) seed
	// doneCardData[runID]; tests that don't care leave it empty and
	// the helper returns a zero rollup (renderer degrades to the short
	// `Ns · N steps` footer).
	doneCardData map[string]store.DoneCardRunData

	// systemNotices records every SendSystemNoticeMessage the driver
	// fires — the dead-letter path writes one when the retry budget is
	// exhausted. Tests inspect this slice to assert "notice was
	// emitted" and "with the right Kind".
	systemNotices []store.SendSystemNoticeMessageInput

	// Credential-form path state. Tests that exercise the form
	// branch populate missingNotices (keyed "conv_id|run_id") and
	// inboundUserMsg (keyed conv_id); the driver's stash write is
	// captured into pendingFormsWritten so tests can assert on the
	// values that landed in the slot. pendingFormStored simulates the
	// persisted slot the insert-or-noop writer would return on a
	// follow-up tick (when set, the WritePendingCredentialFormSlot
	// fake returns this instead of the just-stashed payload — the
	// real DB does the same when a slot already exists).
	// pendingFormMsgIDStamps records UpdatePendingCredentialFormSlotMessageID
	// calls. pendingFormClears records ClearPendingCredentialFormSlotByConversation
	// calls (used by the permanent-patch-failure path).
	missingNotices         map[string][]store.CapabilityCredentialMissingNotice
	inboundUserMsg         map[string]store.InboundUserMessageForRun
	guestReplyHint         map[string]string
	pendingFormsWritten    []pendingFormWrite
	pendingFormStored      map[string]store.PendingCredentialFormSlot
	pendingFormMsgIDStamps []pendingFormMsgIDStamp
	pendingFormClears      []string
	updatePendingFormErr   error

	// Queue-card driver state. Tests that exercise the queue tick
	// seed pendingQueued (returned by ClaimPendingQueuedFeishuRuns)
	// and queuePositions[runID] (returned by QueuePositionForRun).
	// The driver records every StampQueueCardSent into queueStamps
	// so the test can assert "card sent exactly once for run X".
	// queueClaimedBys records each ClaimedBy passed in, so a test
	// can drive two pods through the same fakeStore and assert only
	// one of them got the queued run (the other saw the drained
	// pendingQueued slice).
	pendingQueued   []store.PendingQueuedFeishuRun
	queuePositions  map[string]int
	queueCutoffs    []time.Time
	queueClaimedBys []string
	queueStamps     []string
	queuePosErrs    map[string]error

	// agentNameByConversation feeds ResolveAgentNameForConversation
	// for the auto-expire permission card path. Tests that don't
	// care leave the map nil and the stub returns "" (the production
	// fallback to FeishuCardTitle in the builder).
	agentNameByConversation map[string]string
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		agents:              make(map[string]store.FeishuAgentRoute),
		secrets:             make(map[string]store.SecretPayload),
		inflightEvents:      make(map[string][]store.AgentRunEvent),
		cardsByConv:         make(map[string]store.ConversationInflightCards),
		cardsByPermReq:      make(map[string]store.ConversationInflightCards),
		cardsByPromptForUserChoiceReq: make(map[string]store.ConversationInflightCards),
		reactionsByConv:     make(map[string]store.FeishuInboundReactionRow),
		reactionsByAgentRun: make(map[string]store.FeishuInboundReactionRow),
		doneCardData:        make(map[string]store.DoneCardRunData),
	}
}

func (f *fakeStore) MarkGatewayOutboundDelivered(_ context.Context, input store.MarkGatewayOutboundDeliveredInput) (store.MarkGatewayOutboundDeliveredResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.markErrIdx < len(f.markErrSequence) {
		err := f.markErrSequence[f.markErrIdx]
		f.markErrIdx++
		if err != nil {
			return store.MarkGatewayOutboundDeliveredResult{}, err
		}
	}
	f.delivered = append(f.delivered, input)
	return store.MarkGatewayOutboundDeliveredResult{MessageID: input.MessageID}, nil
}

func (f *fakeStore) GetAgentByFeishuAppID(_ context.Context, appID string) (store.FeishuAgentRoute, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.agentErr != nil {
		return store.FeishuAgentRoute{}, f.agentErr
	}
	route, ok := f.agents[appID]
	if !ok {
		return store.FeishuAgentRoute{}, store.ErrUnknownFeishuAgent
	}
	return route, nil
}

func (f *fakeStore) GetSecretPayload(_ context.Context, _, secretID string) (store.SecretPayload, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.secretErr != nil {
		return store.SecretPayload{}, f.secretErr
	}
	payload, ok := f.secrets[secretID]
	if !ok {
		return store.SecretPayload{}, errors.New("secret not found")
	}
	return payload, nil
}

// --- Inflight driver stubs ---

func (f *fakeStore) ListActiveFeishuInflightConversations(_ context.Context, cutoff time.Time, _ int32) ([]store.FeishuInflightConversation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inflightCutoffs = append(f.inflightCutoffs, cutoff)
	out := append([]store.FeishuInflightConversation{}, f.inflightConvs...)
	return out, nil
}

// ClaimActiveFeishuInflightConversations mirrors ListActive's
// "return whatever's seeded in inflightConvs" behaviour. The
// real-Postgres concurrency semantics (SELECT ... FOR UPDATE SKIP
// LOCKED + jsonb claim stamp + stale-recovery) are covered by
// store_inflight_claim_test.go against a real DB.
func (f *fakeStore) ClaimActiveFeishuInflightConversations(_ context.Context, input store.ClaimActiveFeishuInflightConversationsInput) ([]store.FeishuInflightConversation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inflightClaimCalls = append(f.inflightClaimCalls, input)
	out := append([]store.FeishuInflightConversation{}, f.inflightConvs...)
	return out, nil
}

func (f *fakeStore) ListAgentRunEventsAfterSeq(_ context.Context, runID string, afterSeq int64, _ int32) ([]store.AgentRunEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	events := f.inflightEvents[runID]
	out := make([]store.AgentRunEvent, 0, len(events))
	for _, ev := range events {
		if ev.Sequence > afterSeq {
			out = append(out, ev)
		}
	}
	return out, nil
}

func (f *fakeStore) UpsertConversationInflightWorkingCard(_ context.Context, input store.UpsertConversationInflightWorkingCardInput) (store.WorkingInflightSlot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inflightUpserts = append(f.inflightUpserts, input)
	// Mirror the SQL guard so first-send against a stale slot fails
	// the way it does in prod, instead of silently succeeding.
	for i := range f.inflightConvs {
		if f.inflightConvs[i].ConversationID != input.ConversationID {
			continue
		}
		current := ""
		if md := f.inflightConvs[i].ConversationMetadata; md != nil {
			if gi, ok := md["gateway_inflight"].(map[string]any); ok {
				if w, ok := gi["working"].(map[string]any); ok {
					if id, ok := w["agent_run_id"].(string); ok {
						current = id
					}
				}
			}
		}
		if current != input.ExpectedOldRunID {
			return store.WorkingInflightSlot{}, store.ErrConversationInflightConflict
		}
		if f.inflightConvs[i].ConversationMetadata == nil {
			f.inflightConvs[i].ConversationMetadata = map[string]any{}
		}
		md := f.inflightConvs[i].ConversationMetadata
		gi, _ := md["gateway_inflight"].(map[string]any)
		if gi == nil {
			gi = map[string]any{}
			md["gateway_inflight"] = gi
		}
		gi["working"] = map[string]any{
			"agent_run_id":    input.Slot.AgentRunID,
			"external_msg_id": input.Slot.ExternalMsgID,
			"seq_emitted":     input.Slot.SeqEmitted,
		}
		break
	}
	return input.Slot, nil
}

func (f *fakeStore) ClearConversationInflightSlot(_ context.Context, conversationID string, slot store.InflightSlotKind, expectedAgentRunID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inflightClears = append(f.inflightClears, fakeInflightClear{ConversationID: conversationID, Slot: slot, ExpectedAgentRunID: expectedAgentRunID})
	return nil
}

func (f *fakeStore) MarkConversationInflightTerminalDelivered(_ context.Context, conversationID, runID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.terminalDeliveredMarks = append(f.terminalDeliveredMarks, fakeTerminalDeliveredMark{
		ConversationID: conversationID,
		RunID:          runID,
	})
	return nil
}

func (f *fakeStore) UpsertConversationInflightPermissionCard(_ context.Context, input store.UpsertConversationInflightPermissionCardInput) (store.PermissionInflightSlot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.permissionUpserts = append(f.permissionUpserts, input)
	return input.Slot, nil
}

func (f *fakeStore) GetConversationInflightCards(_ context.Context, conversationID string) (store.ConversationInflightCards, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if got, ok := f.cardsByConv[conversationID]; ok {
		return got, nil
	}
	return store.ConversationInflightCards{}, store.ErrUnknownConversation
}

func (f *fakeStore) FindConversationByPermissionRequestID(_ context.Context, permissionRequestID string) (store.ConversationInflightCards, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if got, ok := f.cardsByPermReq[permissionRequestID]; ok {
		return got, nil
	}
	return store.ConversationInflightCards{}, store.ErrUnknownConversation
}

func (f *fakeStore) ListStaleFeishuPermissionInflightCards(_ context.Context, cutoff time.Time, _ int32) ([]store.ConversationInflightCards, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.staleCutoffs = append(f.staleCutoffs, cutoff)
	out := append([]store.ConversationInflightCards{}, f.stalePermissions...)
	return out, nil
}

func (f *fakeStore) UpsertConversationInflightPromptForUserChoiceCard(_ context.Context, input store.UpsertConversationInflightPromptForUserChoiceCardInput) (store.PromptForUserChoiceInflightSlot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.promptForUserChoiceUpserts = append(f.promptForUserChoiceUpserts, input)
	return input.Slot, nil
}

func (f *fakeStore) FindConversationByPromptForUserChoiceRequestID(_ context.Context, requestID string) (store.ConversationInflightCards, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if got, ok := f.cardsByPromptForUserChoiceReq[requestID]; ok {
		return got, nil
	}
	return store.ConversationInflightCards{}, store.ErrUnknownConversation
}

func (f *fakeStore) ListStaleFeishuPromptForUserChoiceInflightCards(_ context.Context, _ time.Time, _ int32) ([]store.ConversationInflightCards, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := append([]store.ConversationInflightCards{}, f.stalePromptForUserChoice...)
	return out, nil
}

// Typing-reaction undo path. Tests that exercise the terminal ->
// delete-reaction flow seed reactionsByConv keyed by conversation_id;
// reactionClears captures every ClearFeishuInboundReaction call so the
// test can assert metadata cleanup actually fired.
func (f *fakeStore) FindLatestFeishuInboundReactionByConversation(_ context.Context, conversationID string) (store.FeishuInboundReactionRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.reactionsByConv[conversationID]
	if !ok {
		return store.FeishuInboundReactionRow{}, store.ErrUnknownMessage
	}
	return row, nil
}

// FindFeishuInboundReactionByAgentRun returns the per-run-pinned row when
// the test seeded reactionsByAgentRun[agentRunID]. Default fall-through is
// ErrUnknownMessage so existing tests (which never seed this map)
// transparently exercise the conversation-latest fallback in
// resolveReactionRowForRun.
func (f *fakeStore) FindFeishuInboundReactionByAgentRun(_ context.Context, agentRunID string) (store.FeishuInboundReactionRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	row, ok := f.reactionsByAgentRun[agentRunID]
	if !ok {
		return store.FeishuInboundReactionRow{}, store.ErrUnknownMessage
	}
	return row, nil
}

func (f *fakeStore) ClearFeishuInboundReaction(_ context.Context, messageID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reactionClears = append(f.reactionClears, messageID)
	return nil
}

// LoadDoneCardRunData feeds the DoneCard assembly helper. Tests that
// don't care about footer data leave doneCardData empty — the helper
// degrades to a zero DoneCardRunData (HasUsage=false), which the
// renderer renders as the short `Ns · N steps` footer.
func (f *fakeStore) LoadDoneCardRunData(_ context.Context, _, _, runID string) (store.DoneCardRunData, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if data, ok := f.doneCardData[runID]; ok {
		return data, nil
	}
	return store.DoneCardRunData{}, nil
}

// SendSystemNoticeMessage records the dead-letter notice the driver
// fires when the retry budget is exhausted.
func (f *fakeStore) SendSystemNoticeMessage(_ context.Context, input store.SendSystemNoticeMessageInput) (store.SendSystemNoticeMessageResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.systemNotices = append(f.systemNotices, input)
	return store.SendSystemNoticeMessageResult{MessageID: "msg-system-notice", Created: true}, nil
}

// Credential-form path. The default fake returns empty lists so
// tests that don't exercise the form path stay unaffected; tests that
// need the form-card branch seed missingNotices + inboundUserMsg.
func (f *fakeStore) ListCapabilityCredentialMissingForRun(_ context.Context, conversationID, runID string) ([]store.CapabilityCredentialMissingNotice, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := conversationID + "|" + runID
	return append([]store.CapabilityCredentialMissingNotice(nil), f.missingNotices[key]...), nil
}

func (f *fakeStore) GetInboundUserMessageForRun(_ context.Context, conversationID string, _ string) (store.InboundUserMessageForRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if msg, ok := f.inboundUserMsg[conversationID]; ok {
		return msg, nil
	}
	return store.InboundUserMessageForRun{}, nil
}

func (f *fakeStore) GetGuestReplyHintForRun(_ context.Context, conversationID string, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.guestReplyHint[conversationID], nil
}

// pendingFormWrite captures one WritePendingCredentialFormSlot call
// for assertion in driver tests. Mirrors the legacy
// CreateFeishuCredentialQkeyInput capture but only retains the
// fields tests actually need to verify (slot + conversation routing).
type pendingFormWrite struct {
	ConversationID string
	Slot           store.PendingCredentialFormSlot
}

// pendingFormMsgIDStamp captures one UpdatePendingCredentialFormSlotMessageID
// call. Tests on the patch-on-second-tick path assert the driver
// stamped the om_… onto the right slot after the first SendMessage.
type pendingFormMsgIDStamp struct {
	ConversationID string
	Qkey           string
	ExternalMsgID  string
}

func (f *fakeStore) WritePendingCredentialFormSlot(_ context.Context, conversationID string, slot store.PendingCredentialFormSlot) (store.PendingCredentialFormSlot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pendingFormsWritten = append(f.pendingFormsWritten, pendingFormWrite{
		ConversationID: conversationID,
		Slot:           slot,
	})
	// Insert-or-noop semantics: if a slot was pre-seeded for this
	// conversation, the real DB returns the existing one and discards
	// the new payload. Tests that exercise the "second tick reuses
	// existing slot" path seed pendingFormStored[conversationID].
	if existing, ok := f.pendingFormStored[conversationID]; ok {
		return existing, nil
	}
	if f.pendingFormStored == nil {
		f.pendingFormStored = make(map[string]store.PendingCredentialFormSlot)
	}
	f.pendingFormStored[conversationID] = slot
	return slot, nil
}

func (f *fakeStore) UpdatePendingCredentialFormSlotMessageID(_ context.Context, conversationID, qkey, externalMsgID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.updatePendingFormErr != nil {
		return f.updatePendingFormErr
	}
	f.pendingFormMsgIDStamps = append(f.pendingFormMsgIDStamps, pendingFormMsgIDStamp{
		ConversationID: conversationID,
		Qkey:           qkey,
		ExternalMsgID:  externalMsgID,
	})
	if existing, ok := f.pendingFormStored[conversationID]; ok && existing.Qkey == qkey {
		existing.ExternalMsgID = externalMsgID
		f.pendingFormStored[conversationID] = existing
	}
	return nil
}

func (f *fakeStore) ClearPendingCredentialFormSlotByConversation(_ context.Context, conversationID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pendingFormClears = append(f.pendingFormClears, conversationID)
	delete(f.pendingFormStored, conversationID)
	return nil
}

func (f *fakeStore) ClaimPendingQueuedFeishuRuns(_ context.Context, input store.ClaimPendingQueuedFeishuRunsInput) ([]store.PendingQueuedFeishuRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queueCutoffs = append(f.queueCutoffs, input.Cutoff)
	f.queueClaimedBys = append(f.queueClaimedBys, input.ClaimedBy)
	if len(f.pendingQueued) == 0 {
		return nil, nil
	}
	// Drain on claim: simulate the real query's lock-then-stamp
	// behaviour. A sibling pod on the same tick would see no rows.
	out := make([]store.PendingQueuedFeishuRun, len(f.pendingQueued))
	copy(out, f.pendingQueued)
	f.pendingQueued = nil
	return out, nil
}

func (f *fakeStore) QueuePositionForRun(_ context.Context, runID string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.queuePosErrs[runID]; ok {
		return 0, err
	}
	if pos, ok := f.queuePositions[runID]; ok {
		return pos, nil
	}
	// Default to 1 (the most common case: blocked behind one inflight
	// sibling — the running lane-holder is "currently being served",
	// not counted as someone ahead in the queue; see
	// QueuePositionForRun doc comment). Tests that exercise the
	// position display can override per-run via queuePositions.
	return 1, nil
}

func (f *fakeStore) StampQueueCardSent(_ context.Context, runID string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queueStamps = append(f.queueStamps, runID)
	// Drop from pendingQueued so a second tick in the same test
	// doesn't re-process it (mirrors the production filter on
	// queue_card_sent_at).
	kept := f.pendingQueued[:0]
	for _, r := range f.pendingQueued {
		if r.RunID != runID {
			kept = append(kept, r)
		}
	}
	f.pendingQueued = kept
	return nil
}

// ResolveAgentNameForConversation is the no-op stub for the title-
// fallback path. Tests that exercise the auto-expire permission card
// can populate fakeStore.agentNameByConversation; the rest get an
// empty name and rely on the FeishuCardTitle fallback in the
// builders.
func (f *fakeStore) ResolveAgentNameForConversation(_ context.Context, conversationID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.agentNameByConversation == nil {
		return "", nil
	}
	return f.agentNameByConversation[conversationID], nil
}

type fakeInflightClear struct {
	ConversationID     string
	Slot               store.InflightSlotKind
	ExpectedAgentRunID string
}

// fakeTerminalDeliveredMark records one MarkConversationInflightTerminalDelivered
// call. Tests assert presence + run-id pairing to confirm the
// per-run idempotency fingerprint fired at the end of the terminal
// path even when the messages-side gateway_delivered_at marker was
// skipped (the OutputMessageID == "" case the original code
// silently no-op'd into a re-claim loop).
type fakeTerminalDeliveredMark struct {
	ConversationID string
	RunID          string
}

// fakeDecrypter satisfies SecretDecrypter. The "encryptedPayload" is
// just a JSON map for simplicity; production uses real AES.
type fakeDecrypter struct{}

func (fakeDecrypter) Decrypt(envelopeJSON []byte) (map[string]any, error) {
	var out map[string]any
	if err := json.Unmarshal(envelopeJSON, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// happyAgent + happySecret bootstrap a happy-path setup the worker can
// resolve credentials against. Shared by every test file in this
// package that needs to exercise the credential resolver.
func happyAgent() store.FeishuAgentRoute {
	cfg := map[string]any{
		"connectors": map[string]any{
			"feishu": map[string]any{
				"enabled":                true,
				"app_id":                 "cli_happy",
				"app_secret_ref":         "secret_happy",
				"verification_token_ref": "secret_verify",
			},
		},
	}
	raw, _ := json.Marshal(cfg)
	return store.FeishuAgentRoute{
		AgentID:     "agent-happy",
		WorkspaceID: "ws-1",
		Visibility:  string(gateway.VisibilityWorkspace),
		Config:      raw,
	}
}

func happySecret() store.SecretPayload {
	raw, _ := json.Marshal(map[string]string{"app_secret": "real-app-secret-value"})
	return store.SecretPayload{EncryptedPayload: raw}
}

func TestNewWorker_RequiresStoreAndSecrets(t *testing.T) {
	t.Parallel()
	if _, err := NewWorker(Options{}); err == nil {
		t.Error("expected error when Store is nil")
	}
	if _, err := NewWorker(Options{Store: newFakeStore()}); err == nil {
		t.Error("expected error when Secrets is nil")
	}
}
