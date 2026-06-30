// Package slack — outbound Block Kit rendering (PR #4a).
//
// RenderProgress / RenderTerminal turn the neutral channel.ProgressState /
// channel.TerminalResult the driver computes into a Slack Block Kit message
// payload ({text, blocks}). They are pure functions of their input (the only
// wall-clock read is the RenderProgress "now" default, which the caller pins
// for deterministic output), so golden tests lock the JSON.
//
// Unlike the Feishu adapter — which delegates to existing gateway.Build*Card
// builders to stay byte-identical to the legacy path — Slack has no prior
// renderer, so these builders are net-new. They honor Block Kit limits:
// header plain_text ≤150 chars, section mrkdwn ≤3000 chars, context ≤10
// elements, ≤50 blocks per message, and a top-level `text` fallback (required
// for notifications / accessibility). Streaming text is mrkdwn-formatted via
// the TextCodec (textcodec.go) before chunking.
package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// slackCardMIME tags a rendered Block Kit payload. The driver treats
// Card.Payload opaquely; the MIME lets the multi-platform send path assert it
// is handing the right card shape to the right channel.
const slackCardMIME = "slack/blockkit"

// Block Kit structural limits.
const (
	slackHeaderLimit        = 150  // header plain_text max chars
	slackSectionLimit       = 3000 // section mrkdwn max chars
	slackMaxBlocks          = 50   // blocks per message
	slackMaxContextElements = 10   // elements per context block
)

// defaultCardTitle mirrors the Feishu adapter's empty-title fallback so a
// run with no agent name still renders a header.
const defaultCardTitle = "Agent"

// errCardFallbackMessage mirrors the Feishu adapter's failed-run fallback copy
// (cards.go) so a TerminalResult with an empty ErrorMessage renders the same
// user-facing failure text across platforms.
const errCardFallbackMessage = "Agent 运行失败，请稍后重试。"

// truncatedNote is appended (as a context block) when a card would exceed the
// 50-block limit, so the user knows output was clipped.
const truncatedNote = "… output truncated"

// --- Block Kit wire structs (typed for deterministic JSON key order) --------

type bkTextObject struct {
	Type  string `json:"type"` // "mrkdwn" | "plain_text"
	Text  string `json:"text"`
	Emoji *bool  `json:"emoji,omitempty"` // plain_text only
}

type bkBlock struct {
	Type     string         `json:"type"`               // "header" | "section" | "context"
	Text     *bkTextObject  `json:"text,omitempty"`     // header / section
	Elements []bkTextObject `json:"elements,omitempty"` // context
}

type bkMessage struct {
	Text   string    `json:"text"`
	Blocks []bkBlock `json:"blocks"`
}

// --- interactive Block Kit wire structs (permission / choice / credential) ---
//
// The progress/terminal cards above are display-only (header/section/context).
// The interactive cards add buttons, selects and text inputs, whose JSON shape
// is richer than bkBlock, so they use a parallel struct set rendered into an
// any-typed block list (bkInteractiveMessage). action_id values MUST match the
// keys in slackActionKinds (action.go) so HandleAction maps a click back to the
// right neutral CardActionKind.

// bkOption is one static_select / multi_static_select choice.
type bkOption struct {
	Text  bkTextObject `json:"text"`
	Value string       `json:"value"`
}

// bkElement is an interactive element: a button, a (multi_)static_select, or a
// plain_text_input. Only the fields relevant to its Type are populated.
type bkElement struct {
	Type        string        `json:"type"`
	ActionID    string        `json:"action_id"`
	Text        *bkTextObject `json:"text,omitempty"`        // button label
	Value       string        `json:"value,omitempty"`       // button value (round-tripped id)
	Style       string        `json:"style,omitempty"`       // "primary" | "danger"
	Placeholder *bkTextObject `json:"placeholder,omitempty"` // select / input hint
	Options     []bkOption    `json:"options,omitempty"`     // select choices
	Multiline   bool          `json:"multiline,omitempty"`   // plain_text_input
}

// bkActionsBlock holds buttons / selects in a row.
type bkActionsBlock struct {
	Type     string      `json:"type"` // "actions"
	BlockID  string      `json:"block_id,omitempty"`
	Elements []bkElement `json:"elements"`
}

// bkInputBlock wraps a single text input with its label (credential form).
type bkInputBlock struct {
	Type    string       `json:"type"` // "input"
	BlockID string       `json:"block_id,omitempty"`
	Label   bkTextObject `json:"label"`
	Element bkElement    `json:"element"`
}

// bkInteractiveMessage carries a heterogeneous block list (display blocks plus
// actions/input blocks), so its Blocks field is any-typed.
type bkInteractiveMessage struct {
	Text   string `json:"text"`
	Blocks []any  `json:"blocks"`
}

// toolEmoji maps a canonical tool name to a Slack emoji shortcode for the
// step line. Tools absent from the table fall back to defaultToolEmoji.
var toolEmoji = map[string]string{
	"Bash":      ":computer:",
	"Read":      ":page_facing_up:",
	"Edit":      ":pencil2:",
	"Write":     ":pencil2:",
	"Grep":      ":mag:",
	"Glob":      ":mag:",
	"WebFetch":  ":link:",
	"WebSearch": ":mag:",
	"LSP":       ":computer:",
	"Skill":     ":robot_face:",
	"Agent":     ":computer:",
	"Workflow":  ":robot_face:",
}

const defaultToolEmoji = ":gear:"

func emojiForTool(tool string) string {
	if e, ok := toolEmoji[strings.TrimSpace(tool)]; ok {
		return e
	}
	return defaultToolEmoji
}

// RenderProgress renders the in-flight ("executing") Block Kit card. now
// defaults to time.Now().UTC() when ProgressState.Now is zero (matching the
// Feishu adapter), so still-running step durations advance; callers pin Now
// for deterministic output.
func (c *Channel) RenderProgress(_ context.Context, _ channel.ReplyTarget, state channel.ProgressState) (channel.Card, error) {
	now := state.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	title := resolveTitle(state.Title)

	blocks := []bkBlock{headerBlock(title)}
	blocks = append(blocks, stepContextBlocks(state.Steps, now)...)
	blocks = append(blocks, c.streamingSections(state.StreamingText)...)

	status := ":hourglass_flowing_sand: Running"
	if state.Done {
		status = ":white_check_mark: Done"
	}
	if state.Elapsed > 0 {
		status += " · " + formatDuration(state.Elapsed)
	}
	blocks = append(blocks, contextBlock([]string{status}))

	fallback := title + " is running"
	if state.Done {
		fallback = title + " finished"
	}
	return cardFrom(blocks, fallback)
}

// RenderTerminal renders the terminal Done / Error Block Kit card. Success
// selects the Done path; otherwise renderError builds the failure card with
// the same empty-message fallback the Feishu adapter applies.
func (c *Channel) RenderTerminal(_ context.Context, _ channel.ReplyTarget, result channel.TerminalResult) (channel.Card, error) {
	title := resolveTitle(result.Title)
	if !result.Success {
		return c.renderError(title, result)
	}

	// Terminal steps have ended, so a zero clock yields their final
	// EndedAt-StartedAt duration deterministically (StepInfo.Duration only
	// reaches for `now` on a still-running step, which a terminal card has none
	// of).
	blocks := []bkBlock{headerBlock(title)}
	blocks = append(blocks, stepContextBlocks(result.Steps, time.Time{})...)

	if think := strings.TrimSpace(result.Thinking); think != "" {
		blocks = append(blocks, sectionBlock("*Thinking*\n"+quote(c.Format(think))))
	}
	blocks = append(blocks, c.streamingSections(result.StreamingText)...)
	if result.Usage != nil {
		blocks = append(blocks, contextBlock([]string{usageLine(result.Usage)}))
	}

	fallback := firstLine(strings.TrimSpace(result.StreamingText))
	if fallback == "" {
		fallback = title + " finished"
	}
	return cardFrom(blocks, fallback)
}

// renderError builds the failure card. RawError is rendered in a code block;
// RunDetailURL becomes a context-block deep link; GuestHint is mrkdwn-formatted.
func (c *Channel) renderError(title string, result channel.TerminalResult) (channel.Card, error) {
	msg := strings.TrimSpace(result.ErrorMessage)
	if msg == "" {
		msg = errCardFallbackMessage
	}
	blocks := []bkBlock{
		headerBlock(title),
		sectionBlock(":x: *" + escapeMrkdwn(msg) + "*"),
	}
	if raw := strings.TrimSpace(result.RawError); raw != "" {
		blocks = append(blocks, sectionBlock(codeBlock(raw)))
	}
	if url := strings.TrimSpace(result.RunDetailURL); url != "" {
		blocks = append(blocks, contextBlock([]string{"<" + url + "|View run details>"}))
	}
	if hint := strings.TrimSpace(result.GuestHint); hint != "" {
		blocks = append(blocks, sectionBlock(c.Format(hint)))
	}
	return cardFrom(blocks, title+" — "+msg)
}

// --- interactive cards (permission / choice / credential) -------------------

// RenderPermission renders the Allow/Deny card. The two buttons carry the
// permission_request_id in their value and action_ids that HandleAction maps to
// CardActionPermissionAllow / CardActionPermissionDeny. A plain-text fallback
// keeps the notification readable when blocks are stripped.
func (c *Channel) RenderPermission(_ context.Context, _ channel.ReplyTarget, req channel.PermissionRequest) (channel.Card, error) {
	title := resolveTitle(req.Title)
	tool := strings.TrimSpace(req.ToolName)
	if tool == "" {
		tool = "a tool"
	}

	blocks := []any{
		headerBlock(title),
		sectionBlock(":warning: *" + escapeMrkdwn(tool) + "* needs your approval to run."),
	}
	if input := strings.TrimSpace(req.ToolInput); input != "" {
		blocks = append(blocks, sectionBlock(codeBlock(input)))
	}
	blocks = append(blocks, bkActionsBlock{
		Type:    "actions",
		BlockID: "permission",
		Elements: []bkElement{
			{
				Type:     "button",
				ActionID: "permission_allow",
				Text:     plainText("Allow"),
				Value:    req.RequestID,
				Style:    "primary",
			},
			{
				Type:     "button",
				ActionID: "permission_deny",
				Text:     plainText("Deny"),
				Value:    req.RequestID,
				Style:    "danger",
			},
		},
	})

	fallback := title + " — approve " + tool + "?"
	return interactiveCardFrom(blocks, fallback)
}

// RenderChoiceForm renders the prompt_for_user_choice card: one select per
// question (multi_static_select when MultiSelect) plus a single Submit button
// carrying the request id. The selects use action_id ask_user_choice_pick and
// per-question block_ids so the inbound decode can fold State.Values; the Submit
// button uses ask_user_choice_submit.
func (c *Channel) RenderChoiceForm(_ context.Context, _ channel.ReplyTarget, form channel.ChoiceForm) (channel.Card, error) {
	title := resolveTitle(form.Title)
	blocks := []any{headerBlock(title)}

	for i, q := range form.Questions {
		prompt := strings.TrimSpace(q.Question)
		if prompt == "" {
			prompt = strings.TrimSpace(q.Header)
		}
		blocks = append(blocks, sectionBlock("*"+escapeMrkdwn(prompt)+"*"))

		selectType := "static_select"
		if q.MultiSelect {
			selectType = "multi_static_select"
		}
		opts := make([]bkOption, 0, len(q.Options))
		for _, label := range q.Options {
			opts = append(opts, bkOption{Text: *plainText(label), Value: label})
		}
		blocks = append(blocks, bkActionsBlock{
			Type:    "actions",
			BlockID: fmt.Sprintf("choice_%d", i),
			Elements: []bkElement{
				{
					Type:        selectType,
					ActionID:    "ask_user_choice_pick",
					Placeholder: plainText("Select…"),
					Options:     opts,
				},
			},
		})
	}

	blocks = append(blocks, bkActionsBlock{
		Type:    "actions",
		BlockID: "choice_submit",
		Elements: []bkElement{
			{
				Type:     "button",
				ActionID: "ask_user_choice_submit",
				Text:     plainText("Submit"),
				Value:    form.RequestID,
				Style:    "primary",
			},
		},
	})

	return interactiveCardFrom(blocks, title+" needs your input")
}

// RenderCredentialForm renders the missing-credential input form: one
// plain_text_input per field (block_id = capability name so the inbound decode
// can fold State.Values) plus a Submit button carrying the minted qkey under
// action_id credential_form_submit.
func (c *Channel) RenderCredentialForm(_ context.Context, _ channel.ReplyTarget, form channel.CredentialForm) (channel.Card, error) {
	title := resolveTitle(form.Title)
	blocks := []any{
		headerBlock(title),
		sectionBlock(":key: This run needs credentials before it can continue."),
	}

	for i, f := range form.Fields {
		label := strings.TrimSpace(f.Label)
		if label == "" {
			label = strings.TrimSpace(f.CapabilityName)
		}
		if label == "" {
			label = fmt.Sprintf("Field %d", i+1)
		}
		blockID := strings.TrimSpace(f.CapabilityName)
		if blockID == "" {
			blockID = fmt.Sprintf("cred_%d", i)
		}
		el := bkElement{Type: "plain_text_input", ActionID: "credential_value"}
		if ph := strings.TrimSpace(f.Placeholder); ph != "" {
			el.Placeholder = plainText(ph)
		}
		blocks = append(blocks, bkInputBlock{
			Type:    "input",
			BlockID: blockID,
			Label:   *plainText(label),
			Element: el,
		})
	}

	blocks = append(blocks, bkActionsBlock{
		Type:    "actions",
		BlockID: "credential_submit",
		Elements: []bkElement{
			{
				Type:     "button",
				ActionID: "credential_form_submit",
				Text:     plainText("Submit"),
				Value:    form.Qkey,
				Style:    "primary",
			},
		},
	})

	return interactiveCardFrom(blocks, title+" needs credentials")
}

// plainText builds a Block Kit plain_text object with emoji enabled (matching
// headerBlock), returned by pointer so it slots into bkElement/bkOption fields.
func plainText(text string) *bkTextObject {
	emoji := true
	return &bkTextObject{Type: "plain_text", Text: text, Emoji: &emoji}
}

// interactiveCardFrom marshals a heterogeneous block list (display + actions +
// input blocks) into a Block Kit message, enforcing the same 50-block cap as
// cardFrom and attaching the notification fallback.
func interactiveCardFrom(blocks []any, fallback string) (channel.Card, error) {
	if len(blocks) > slackMaxBlocks {
		kept := append([]any(nil), blocks[:slackMaxBlocks-1]...)
		kept = append(kept, contextBlock([]string{truncatedNote}))
		blocks = kept
	}
	payload, err := json.Marshal(bkInteractiveMessage{Text: fallback, Blocks: blocks})
	if err != nil {
		return channel.Card{}, err
	}
	return channel.Card{MIME: slackCardMIME, Payload: payload}, nil
}

// --- block builders ---------------------------------------------------------

// renderActionResultCard renders the neutral post-click ActionResultCard
// (permission verdict, credential reject/submitted, user-choice done) into a
// Block Kit replacement message — the Slack twin of the Feishu manager's
// inline result cards. It is a pure function of its input so renderSlackAck can
// marshal it into a replace_original reply. An unrecognised Kind falls back to
// the Summary line (or just the header).
func renderActionResultCard(result *channel.ActionResultCard) bkMessage {
	title := resolveTitle(result.Title)
	blocks := []bkBlock{headerBlock(title)}
	fallback := title

	switch result.Kind {
	case channel.CardActionPermissionAllow, channel.CardActionPermissionDeny:
		if result.Approved {
			blocks = append(blocks, sectionBlock(":white_check_mark: *Approved*"))
			fallback = title + " — approved"
		} else {
			blocks = append(blocks, sectionBlock(":x: *Denied*"))
			fallback = title + " — denied"
		}
	case channel.CardActionCredentialSubmit:
		if result.Rejected {
			reason := strings.TrimSpace(result.RejectReason)
			if reason == "" {
				reason = "Submission rejected."
			}
			blocks = append(blocks, sectionBlock(":no_entry: *"+escapeMrkdwn(reason)+"*"))
			fallback = title + " — " + reason
		} else {
			summary := strings.TrimSpace(result.Summary)
			if summary == "" {
				summary = "Credentials saved."
			}
			blocks = append(blocks, sectionBlock(":white_check_mark: "+escapeMrkdwn(summary)))
			fallback = title + " — " + summary
		}
	default:
		if summary := strings.TrimSpace(result.Summary); summary != "" {
			blocks = append(blocks, sectionBlock(escapeMrkdwn(summary)))
			fallback = title + " — " + summary
		}
	}
	return bkMessage{Text: fallback, Blocks: blocks}
}

func headerBlock(title string) bkBlock {
	emoji := true
	return bkBlock{
		Type: "header",
		Text: &bkTextObject{Type: "plain_text", Text: truncateRunes(title, slackHeaderLimit), Emoji: &emoji},
	}
}

func sectionBlock(mrkdwn string) bkBlock {
	return bkBlock{Type: "section", Text: &bkTextObject{Type: "mrkdwn", Text: mrkdwn}}
}

func contextBlock(lines []string) bkBlock {
	els := make([]bkTextObject, 0, len(lines))
	for _, l := range lines {
		els = append(els, bkTextObject{Type: "mrkdwn", Text: l})
	}
	return bkBlock{Type: "context", Elements: els}
}

// stepContextBlocks renders the folded tool steps as context blocks, batching
// at most slackMaxContextElements step lines per block.
func stepContextBlocks(steps []gateway.StepInfo, now time.Time) []bkBlock {
	if len(steps) == 0 {
		return nil
	}
	var blocks []bkBlock
	batch := make([]string, 0, slackMaxContextElements)
	for _, s := range steps {
		batch = append(batch, stepLine(s, now))
		if len(batch) == slackMaxContextElements {
			blocks = append(blocks, contextBlock(batch))
			batch = make([]string, 0, slackMaxContextElements)
		}
	}
	if len(batch) > 0 {
		blocks = append(blocks, contextBlock(batch))
	}
	return blocks
}

func stepLine(s gateway.StepInfo, now time.Time) string {
	label := strings.TrimSpace(s.Label)
	if label == "" {
		label = strings.TrimSpace(s.Tool)
	}
	line := emojiForTool(s.Tool) + " " + escapeMrkdwn(label)
	if d := s.Duration(now); d > 0 {
		line += " · " + formatDuration(d)
	}
	return line
}

// streamingSections mrkdwn-formats the assistant text and splits it into
// section blocks each within the 3000-char limit. Empty text yields no blocks.
func (c *Channel) streamingSections(text string) []bkBlock {
	t := strings.TrimSpace(text)
	if t == "" {
		return nil
	}
	chunks := chunkRunes(c.Format(t), slackSectionLimit)
	blocks := make([]bkBlock, 0, len(chunks))
	for _, ch := range chunks {
		blocks = append(blocks, sectionBlock(ch))
	}
	return blocks
}

// cardFrom marshals blocks into a Block Kit message, enforcing the 50-block
// cap (clipping with a truncation note) and attaching the fallback text.
func cardFrom(blocks []bkBlock, fallback string) (channel.Card, error) {
	if len(blocks) > slackMaxBlocks {
		kept := append([]bkBlock(nil), blocks[:slackMaxBlocks-1]...)
		kept = append(kept, contextBlock([]string{truncatedNote}))
		blocks = kept
	}
	payload, err := json.Marshal(bkMessage{Text: fallback, Blocks: blocks})
	if err != nil {
		return channel.Card{}, err
	}
	return channel.Card{MIME: slackCardMIME, Payload: payload}, nil
}

// --- small helpers ----------------------------------------------------------

func resolveTitle(title string) string {
	if t := strings.TrimSpace(title); t != "" {
		return t
	}
	return defaultCardTitle
}

func usageLine(u *gateway.UsageStats) string {
	parts := make([]string, 0, 3)
	if u.CostUSD > 0 {
		parts = append(parts, fmt.Sprintf(":moneybag: $%.2f", u.CostUSD))
	}
	if u.ContextWindow > 0 {
		parts = append(parts, fmt.Sprintf(":bar_chart: %d/%d tokens", u.ContextUsed, u.ContextWindow))
	}
	if m := strings.TrimSpace(u.Model); m != "" {
		parts = append(parts, escapeMrkdwn(m))
	}
	if len(parts) == 0 {
		return ":information_source: usage unavailable"
	}
	return strings.Join(parts, " · ")
}

// formatDuration renders a wall-clock duration as a stable "12s" / "1m30s".
func formatDuration(d time.Duration) string {
	secs := int(d.Round(time.Second) / time.Second)
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	return fmt.Sprintf("%dm%ds", secs/60, secs%60)
}

// codeBlock wraps text in a Slack code fence, truncated to fit a section.
func codeBlock(s string) string {
	const fence = "```"
	body := truncateRunes(s, slackSectionLimit-2*len(fence)-2)
	return fence + "\n" + body + "\n" + fence
}

// quote prefixes each line with Slack's blockquote marker.
func quote(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = "> " + l
	}
	return strings.Join(lines, "\n")
}

// firstLine returns the first line of s (used as a notification fallback).
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

// truncateRunes cuts s to at most max runes, appending an ellipsis when cut.
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 1 {
		return string(r[:max])
	}
	return string(r[:max-1]) + "…"
}

// chunkRunes splits s into pieces of at most limit runes, preferring to break
// at a newline in the back half of each piece so lines stay intact.
func chunkRunes(s string, limit int) []string {
	r := []rune(s)
	if len(r) <= limit {
		return []string{s}
	}
	var out []string
	for len(r) > limit {
		cut := limit
		for i := limit; i > limit/2; i-- {
			if r[i-1] == '\n' {
				cut = i
				break
			}
		}
		out = append(out, string(r[:cut]))
		r = r[cut:]
	}
	if len(r) > 0 {
		out = append(out, string(r))
	}
	return out
}
