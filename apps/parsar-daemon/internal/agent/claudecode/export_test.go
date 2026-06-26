package claudecode

// This file uses _test.go so it only compiles into the test binary,
// but lives in the production package — re-exports internal symbols
// for the external claudecode_test package without polluting the
// public surface.

import "github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"

func NewPendingTableForTest() *PendingTable { return (*PendingTable)(newPendingTable()) }

// PendingTable is the test-visible alias for pendingTable.
type PendingTable pendingTable

type PendingEntry = pendingEntry

func (p *PendingTable) Record(permID, ccRequestID string, input map[string]any) {
	(*pendingTable)(p).Record(permID, ccRequestID, input)
}
func (p *PendingTable) Resolve(permID string) (PendingEntry, bool) {
	return (*pendingTable)(p).Resolve(permID)
}
func (p *PendingTable) LookupByCC(ccReq string) (string, bool) {
	return (*pendingTable)(p).LookupByCC(ccReq)
}
func (p *PendingTable) Delete(permID string) { (*pendingTable)(p).Delete(permID) }
func (p *PendingTable) Len() int             { return (*pendingTable)(p).Len() }

// PendingAskTable is the test-visible alias for pendingAskTable.
type PendingAskTable pendingAskTable

type PendingAskEntry = pendingAskEntry

func NewPendingAskTableForTest() *PendingAskTable {
	return (*PendingAskTable)(newPendingAskTable())
}

func (p *PendingAskTable) Record(askID, toolUseID string, questions []proto.PromptForUserChoiceQuestion) {
	(*pendingAskTable)(p).Record(askID, toolUseID, questions)
}
func (p *PendingAskTable) RecordControl(askID, ccRequestID string, questions []proto.PromptForUserChoiceQuestion) {
	(*pendingAskTable)(p).RecordControl(askID, ccRequestID, questions)
}
func (p *PendingAskTable) Take(askID string) (PendingAskEntry, bool) {
	return (*pendingAskTable)(p).Take(askID)
}
func (p *PendingAskTable) Peek(askID string) (PendingAskEntry, bool) {
	return (*pendingAskTable)(p).Peek(askID)
}
func (p *PendingAskTable) Delete(askID string) { (*pendingAskTable)(p).Delete(askID) }
func (p *PendingAskTable) Len() int            { return (*pendingAskTable)(p).Len() }

// NewTranslatorForTest constructs a translator with a deterministic
// perm-id minter. askPending and askMint default to nil — covers the
// legacy permission-only callers; pass via NewTranslatorWithAskForTest
// when exercising the AskUserQuestion interception path.
func NewTranslatorForTest(runID string, pending *PendingTable, mint func() string) *Translator {
	t := newTranslator(runID, (*pendingTable)(pending), nil, permIDMinter(mint), nil)
	return (*Translator)(t)
}

// NewTranslatorWithAskForTest is the ask-aware variant.
func NewTranslatorWithAskForTest(runID string, pending *PendingTable, askPending *PendingAskTable, mint func() string, askMint func() string) *Translator {
	t := newTranslator(runID, (*pendingTable)(pending), (*pendingAskTable)(askPending), permIDMinter(mint), askIDMinter(askMint))
	return (*Translator)(t)
}

type Translator translator

type Translation = translation

func (t *Translator) Translate(line []byte) (Translation, error) {
	return (*translator)(t).Translate(line)
}

// Re-export proto types for the external test package.
type (
	Envelope = proto.Envelope
)

func BuildUserMessageForTest(prompt string, attachments []proto.PromptAttachment) ([]byte, error) {
	return buildUserMessageWithAttachments(prompt, attachments)
}

// BuildAskUserToolResultForTest exposes the daemon-side tool_result
// builder so ask_test.go can lock in the JSON shape claude's stdin
// expects.
func BuildAskUserToolResultForTest(entry PendingAskEntry, decision proto.PromptForUserChoiceDecisionPayload) ([]byte, error) {
	return buildAskUserToolResult(entry, decision)
}

// BuildAskUserControlResponseForTest exposes the control_request-path
// writeback builder so ask_test.go can pin the control_response shape
// claude's stdin expects under --permission-prompt-tool stdio.
func BuildAskUserControlResponseForTest(entry PendingAskEntry, decision proto.PromptForUserChoiceDecisionPayload) ([]byte, error) {
	return buildAskUserControlResponse(entry, decision)
}
