package claudecode

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// askUserQuestionToolName matches Claude Code's built-in tool name.
// We intercept it because it expects a tool_result the human composes,
// and Claude Code's stdin loop has no other way to deliver one.
const askUserQuestionToolName = "AskUserQuestion"

// pendingAskTable maps daemon-minted ask_<8hex> ids to the originating
// Claude Code tool_use id and the structured question payload. The
// daemon keeps both directions so SubmitPromptForUserChoice can write
// a matching tool_result back into the agent's stdin, and so a future
// cancel path (Session.Cancel mid-ask) can clean up by ask id.
//
// AskUserQuestion is a normal tool_use frame on the claude side, not a
// control_request — so the table sits beside pendingTable rather than
// extending it. Same code shape, different keying.
type pendingAskTable struct {
	mu       sync.Mutex
	byAskID  map[string]pendingAskEntry
	byToolID map[string]string
}

type pendingAskEntry struct {
	// ToolUseID is set when AskUserQuestion arrives via the
	// assistant→tool_use path. Empty when it arrived via control_request.
	ToolUseID string

	// CCRequestID is set when AskUserQuestion arrives via the
	// control_request path (claude-code under --permission-prompt-tool
	// stdio wraps the tool in a can_use_tool permission check). Empty
	// when it arrived via tool_use. SubmitPromptForUserChoice picks the
	// reply shape based on which one is set: tool_result for tool_use,
	// control_response for control_request.
	CCRequestID string

	// Questions snapshots the question list this ask was raised for so
	// the writeback can echo header→answer back to the model in the
	// same shape Claude's built-in handler emits. Length >= 1.
	Questions []proto.PromptForUserChoiceQuestion
}

// reverseKey returns whichever id was used to populate byToolID so
// Take / Delete can drop the reverse mapping without caring which
// path recorded the entry.
func (e pendingAskEntry) reverseKey() string {
	if e.ToolUseID != "" {
		return e.ToolUseID
	}
	return e.CCRequestID
}

func newPendingAskTable() *pendingAskTable {
	return &pendingAskTable{
		byAskID:  make(map[string]pendingAskEntry),
		byToolID: make(map[string]string),
	}
}

// Record links a freshly minted ask id to the originating tool_use id
// and the question snapshot. Used by the assistant tool_use path.
func (p *pendingAskTable) Record(askID, toolUseID string, questions []proto.PromptForUserChoiceQuestion) {
	if askID == "" || toolUseID == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.byAskID[askID] = pendingAskEntry{ToolUseID: toolUseID, Questions: questions}
	p.byToolID[toolUseID] = askID
}

// RecordControl links a freshly minted ask id to the originating CC
// request_id (control_request path). Used when claude-code wraps
// AskUserQuestion as a can_use_tool permission check.
func (p *pendingAskTable) RecordControl(askID, ccRequestID string, questions []proto.PromptForUserChoiceQuestion) {
	if askID == "" || ccRequestID == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.byAskID[askID] = pendingAskEntry{CCRequestID: ccRequestID, Questions: questions}
	// Re-use byToolID as a generic reverse map: keyed by either
	// tool_use_id or cc_request_id depending on path. The two id
	// namespaces don't collide (tool_use ids look like toolu_..., cc
	// ones are UUIDs).
	p.byToolID[ccRequestID] = askID
}

// Take atomically reads + deletes the entry recorded for askID. Used by
// SubmitPromptForUserChoice so two near-simultaneous callers (timer-
// fired cancel racing a server-delivered answer) can't both pass and
// each write a tool_result. The loser sees ok=false and returns
// ErrUnknownAsk instead.
//
// Trade-off vs Resolve+Delete: if the subsequent stdin write fails, the
// entry is already gone — a retry surfaces ErrUnknownAsk rather than
// re-doing the write. The double-fire risk is the bigger hazard here
// (timer + server can both reach Submit; stdin flakes are rare and the
// session is going to die anyway when stdin errors), so we accept it.
func (p *pendingAskTable) Take(askID string) (pendingAskEntry, bool) {
	if askID == "" {
		return pendingAskEntry{}, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.byAskID[askID]
	if !ok {
		return pendingAskEntry{}, false
	}
	delete(p.byAskID, askID)
	if key := e.reverseKey(); key != "" {
		delete(p.byToolID, key)
	}
	return e, true
}

// Peek returns the entry without removing it. Reserved for diagnostic /
// test paths that want to inspect pending state without consuming it.
func (p *pendingAskTable) Peek(askID string) (pendingAskEntry, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.byAskID[askID]
	return e, ok
}

// LookupByToolUse reverses the mapping. Reserved for the (currently
// unimplemented) "claude internally cancels its own tool" path.
func (p *pendingAskTable) LookupByToolUse(toolUseID string) (string, bool) {
	if toolUseID == "" {
		return "", false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	askID, ok := p.byToolID[toolUseID]
	return askID, ok
}

// Delete removes both directions for askID.
func (p *pendingAskTable) Delete(askID string) {
	if askID == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.byAskID[askID]
	if !ok {
		return
	}
	delete(p.byAskID, askID)
	if key := e.reverseKey(); key != "" {
		delete(p.byToolID, key)
	}
}

// Len reports the number of outstanding ask requests.
func (p *pendingAskTable) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.byAskID)
}

// interceptAskUserQuestion handled the tool_use path historically. It's
// retained as exported test scaffolding (legacy unit tests still cover
// the case-by-case payload parsing) — translateAssistant no longer
// calls it. See parser.go's tool_use branch comment.
//
// Returns ok=false (no envelope, fall through to a normal TypeToolCall)
// when askPending / askMint are missing, the input shape doesn't fit,
// or the tool_use id is empty.
func (t *translator) interceptAskUserQuestion(toolUseID string, input map[string]any) (proto.Envelope, bool) {
	if t.askPending == nil || t.askMint == nil {
		return proto.Envelope{}, false
	}
	if toolUseID == "" {
		return proto.Envelope{}, false
	}
	questions, ok := parseAskUserQuestionInput(input)
	if !ok {
		return proto.Envelope{}, false
	}

	askID := t.askMint()
	t.askPending.Record(askID, toolUseID, questions)

	env, err := proto.NewEnvelope(proto.TypePromptForUserChoice, t.runID, proto.PromptForUserChoicePayload{
		AskID:     askID,
		Questions: questions,
		ToolUseID: toolUseID,
	})
	if err != nil {
		return proto.Envelope{}, false
	}
	return env, true
}

// interceptAskUserQuestionFromControlRequest is the control_request-
// path twin. Under --permission-prompt-tool stdio, claude-code wraps
// AskUserQuestion as a can_use_tool permission check instead of
// emitting a normal tool_use frame, so the interception entry point is
// different and the writeback shape (control_response) differs from
// the tool_use path (tool_result).
//
// ccRequestID is the SDK's request_id we'll echo back in the
// control_response. ToolUseID stays empty in the envelope payload —
// there's no tool_use_id available on this path.
func (t *translator) interceptAskUserQuestionFromControlRequest(ccRequestID string, input map[string]any) (proto.Envelope, bool) {
	if t.askPending == nil || t.askMint == nil {
		return proto.Envelope{}, false
	}
	if ccRequestID == "" {
		return proto.Envelope{}, false
	}
	questions, ok := parseAskUserQuestionInput(input)
	if !ok {
		return proto.Envelope{}, false
	}

	askID := t.askMint()
	t.askPending.RecordControl(askID, ccRequestID, questions)

	env, err := proto.NewEnvelope(proto.TypePromptForUserChoice, t.runID, proto.PromptForUserChoicePayload{
		AskID:     askID,
		Questions: questions,
	})
	if err != nil {
		return proto.Envelope{}, false
	}
	return env, true
}

// parseAskUserQuestionInput pulls the AskUserQuestion fields out of
// the raw tool input. The schema mirrors Claude Code's built-in:
//
//	{
//	  "questions": [{
//	    "header":  "...",
//	    "question": "...",
//	    "multiSelect": false,
//	    "options": [{"label": "...", "description": "..."}, ...]
//	  }, ...]
//	}
//
// Returns ok=false (fall through to a normal TypeToolCall) when the
// shape doesn't fit. Any single question with an empty question text
// or zero options invalidates the whole call — we'd rather let claude
// see the raw tool_use and re-emit than render a half-broken card.
func parseAskUserQuestionInput(input map[string]any) ([]proto.PromptForUserChoiceQuestion, bool) {
	rawQuestions, exists := input["questions"]
	if !exists {
		return nil, false
	}
	list, isList := rawQuestions.([]any)
	if !isList || len(list) == 0 {
		return nil, false
	}

	out := make([]proto.PromptForUserChoiceQuestion, 0, len(list))
	for _, rawEntry := range list {
		q, isMap := rawEntry.(map[string]any)
		if !isMap {
			return nil, false
		}
		question, _ := q["question"].(string)
		header, _ := q["header"].(string)
		multiSelect, _ := q["multiSelect"].(bool)

		rawOptions, _ := q["options"].([]any)
		options := make([]proto.PromptForUserChoiceOption, 0, len(rawOptions))
		for _, raw := range rawOptions {
			om, isOptMap := raw.(map[string]any)
			if !isOptMap {
				continue
			}
			label, _ := om["label"].(string)
			if label == "" {
				continue
			}
			description, _ := om["description"].(string)
			options = append(options, proto.PromptForUserChoiceOption{
				Label:       label,
				Description: description,
			})
		}
		if question == "" || len(options) == 0 {
			return nil, false
		}
		out = append(out, proto.PromptForUserChoiceQuestion{
			Header:      header,
			Question:    question,
			MultiSelect: multiSelect,
			Options:     options,
		})
	}
	return out, true
}

// buildAskUserToolResult turns the human's decision into the NDJSON
// frame the daemon writes back to claude's stdin. The shape is a
// stock Claude Code "user message" carrying a tool_result block —
// claude's SDK then closes the loop on the original tool_use as if
// the local handler had returned the result.
//
// Body shape (one line, no trailing comma):
//
//	{"type":"user","message":{"content":[
//	  {"type":"tool_result","tool_use_id":"<id>",
//	   "content":[{"type":"text","text":"<json or sentence>"}],
//	   "is_error":false}
//	]}}
//
// For a normal answer we encode {"questions":[{"header":..,"answer":..}]}
// — same shape Claude's built-in AskUserQuestion handler emits — so the
// model treats the daemon-mediated path identically to the local one.
//
// For Cancelled answers (timeout, operator stop) we send a plain
// sentence with is_error=false. is_error=true would invite the model
// to retry the same AskUserQuestion call right away, which is exactly
// the deadlock we're trying to avoid.
func buildAskUserToolResult(entry pendingAskEntry, decision proto.PromptForUserChoiceDecisionPayload) ([]byte, error) {
	text := formatAskUserResultText(entry, decision)
	body, err := json.Marshal(map[string]any{
		"type": "user",
		"message": map[string]any{
			"content": []map[string]any{{
				"type":        "tool_result",
				"tool_use_id": entry.ToolUseID,
				"content": []map[string]any{{
					"type": "text",
					"text": text,
				}},
				"is_error": false,
			}},
		},
	})
	if err != nil {
		return nil, err
	}
	return append(body, '\n'), nil
}

// buildAskUserControlResponse is the control_request-path twin of
// buildAskUserToolResult. Used when claude-code's
// --permission-prompt-tool stdio mode wrapped AskUserQuestion in a
// can_use_tool permission check — the daemon has to respond on the
// control_response channel, not via a user message.
//
// We deny the "permission" (claude won't actually invoke its own
// AskUserQuestion local handler) but pass the human's answer through
// the message field. The SDK surfaces this message to the model as
// the tool_result, closing the loop the same way as the user-message
// path. Body shape:
//
//	{"type":"control_response","response":{
//	  "subtype":"success","request_id":"<cc_request_id>",
//	  "response":{"behavior":"deny","message":"<answer text>"}}}
func buildAskUserControlResponse(entry pendingAskEntry, decision proto.PromptForUserChoiceDecisionPayload) ([]byte, error) {
	text := formatAskUserResultText(entry, decision)
	body, err := json.Marshal(map[string]any{
		"type": "control_response",
		"response": map[string]any{
			"subtype":    "success",
			"request_id": entry.CCRequestID,
			"response": map[string]any{
				"behavior": "deny",
				"message":  text,
			},
		},
	})
	if err != nil {
		return nil, err
	}
	return append(body, '\n'), nil
}

// formatAskUserResultText is the shared text-formatting helper, split
// out so tests can lock the wire shape without re-marshalling the
// whole envelope.
func formatAskUserResultText(entry pendingAskEntry, decision proto.PromptForUserChoiceDecisionPayload) string {
	if decision.Cancelled {
		reason := strings.TrimSpace(decision.Reason)
		switch reason {
		case "timeout":
			return "The user did not make a selection within 10 minutes. Stop the current operation, report the timeout to the user and ask about follow-up intent; do not retry this tool."
		case "cancelled":
			return "The user cancelled this operation. Stop follow-up actions."
		default:
			return "The user did not give a selection (" + reason + "). Stop follow-up actions and wait for further instructions from the user."
		}
	}

	// Multi-question path: pair answers with questions by INDEX, not by
	// Header. Two questions can share the same Header (or both be blank
	// — claude-code's AskUserQuestion treats `header` as optional), so a
	// header-keyed map would collapse them and feed the model the wrong
	// answer. inbound builds QuestionAnswers in the same order as
	// entry.Questions, so positional indexing is the source of truth.
	if len(entry.Questions) > 0 {
		out := make([]map[string]any, 0, len(entry.Questions))
		anyAnswer := false
		// Single-question + legacy Answers slice with multiple entries
		// = the multi-select case the old callback shape used. Join
		// them with the same "、" we render to the human so the model
		// sees one merged answer string for that question.
		if len(entry.Questions) == 1 && len(decision.QuestionAnswers) == 0 && len(decision.Answers) > 1 {
			merged := strings.Join(decision.Answers, "、")
			return mustMarshalAskQuestions([]map[string]any{{
				"header": entry.Questions[0].Header,
				"answer": merged,
			}})
		}
		for i, q := range entry.Questions {
			answer := ""
			if i < len(decision.QuestionAnswers) {
				answer = decision.QuestionAnswers[i].Answer
			}
			// Legacy callback path: a single-question slot answered via
			// the flat Answers slice. Multi-question slots always populate
			// QuestionAnswers, so this branch is no-op for them.
			if answer == "" && len(decision.Answers) > i {
				answer = decision.Answers[i]
			}
			if answer != "" {
				anyAnswer = true
			}
			out = append(out, map[string]any{
				"header": q.Header,
				"answer": answer,
			})
		}
		if !anyAnswer {
			// Treat as cancel; the operator effectively chose nothing.
			return "The user did not choose any option. Stop follow-up actions and wait for further instructions from the user."
		}
		return mustMarshalAskQuestions(out)
	}

	// Legacy fallback: pendingAskEntry has no Questions snapshot
	// (e.g. test scaffold that didn't pass one). Re-emit whatever
	// Answers carries as a single-question payload so the model still
	// receives a parseable result.
	answers := decision.Answers
	if len(answers) == 0 {
		return "The user did not choose any option. Stop follow-up actions and wait for further instructions from the user."
	}
	answer := answers[0]
	if len(answers) > 1 {
		answer = strings.Join(answers, "、")
	}
	return mustMarshalAskQuestions([]map[string]any{{
		"header": "",
		"answer": answer,
	}})
}

func mustMarshalAskQuestions(qs []map[string]any) string {
	payload, _ := json.Marshal(map[string]any{"questions": qs})
	return string(payload)
}
