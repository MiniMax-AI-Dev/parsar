// Package teams — outbound Adaptive Card rendering.
//
// RenderProgress / RenderTerminal / RenderPermission / RenderChoiceForm /
// RenderCredentialForm turn the neutral driver state into an Adaptive Card
// "content" object, wrapped in the teamsWireMessage {text, card} payload the
// outbound transport (outbound.go) attaches to a Bot Framework Activity. They
// are pure functions of their input (the only wall-clock read is the
// RenderProgress "now" default, pinned by the caller for deterministic output),
// so golden tests can lock the JSON.
//
// Teams differs from Slack's Block Kit in two ways this file is built around:
//   - A TextBlock renders a Markdown subset, so streaming text is Format-ed
//     (textcodec.go) rather than translated to a bespoke markup.
//   - Interactive inputs (Input.Text / Input.ChoiceSet) submit their values by
//     `id` merged into the Action.Submit `data` object, so the input ids ARE
//     the neutral FormValues keys ("q<idx>", "credential_<kind>") HandleAction
//     reads back — no separate State.Values fold (unlike Slack).
package teams

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// teamsCardMIME tags a rendered Adaptive Card payload. The driver treats
// Card.Payload opaquely; the MIME lets the send path assert the shape.
const teamsCardMIME = "teams/adaptive"

const (
	adaptiveCardSchema  = "http://adaptivecards.io/schemas/adaptive-card.json"
	adaptiveCardVersion = "1.4"
)

// defaultCardTitle mirrors the Feishu/Slack empty-title fallback so a run with
// no agent name still renders a header.
const defaultCardTitle = "Agent"

// errCardFallbackMessage mirrors the shared failed-run copy so a TerminalResult
// with an empty ErrorMessage renders the same user-facing failure text.
const errCardFallbackMessage = "Agent 运行失败，请稍后重试。"

// --- Adaptive Card wire structs (typed for deterministic JSON key order) -----

// acNode is one Adaptive Card body element. Only the fields relevant to its
// Type are populated; omitempty keeps the JSON tight and stable.
type acNode struct {
	Type string `json:"type"`

	// TextBlock
	Text     string `json:"text,omitempty"`
	Weight   string `json:"weight,omitempty"` // "Bolder"
	Size     string `json:"size,omitempty"`   // "Large" | "Small"
	Wrap     bool   `json:"wrap,omitempty"`
	Color    string `json:"color,omitempty"`    // "Attention" | "Good" | "Warning"
	IsSubtle bool   `json:"isSubtle,omitempty"` // muted step / usage lines
	FontType string `json:"fontType,omitempty"` // "Monospace" for a code preview

	// Input.Text
	ID          string `json:"id,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
	IsMultiline bool   `json:"isMultiline,omitempty"`

	// Input.ChoiceSet
	IsMultiSelect bool       `json:"isMultiSelect,omitempty"`
	ChoiceStyle   string     `json:"style,omitempty"` // "expanded" | "compact"
	Choices       []acChoice `json:"choices,omitempty"`
}

// acChoice is one Input.ChoiceSet option.
type acChoice struct {
	Title string `json:"title"`
	Value string `json:"value"`
}

// acAction is an Adaptive Card action (only Action.Submit is used). Data is
// merged with every input's id→value on submit and arrives as the message
// activity's `value`, so it carries the neutral routing keys HandleAction reads.
type acAction struct {
	Type  string         `json:"type"`
	Title string         `json:"title"`
	Style string         `json:"style,omitempty"` // "positive" | "destructive"
	Data  map[string]any `json:"data,omitempty"`
}

// adaptiveCard is the card "content" object attached to an Activity.
type adaptiveCard struct {
	Type    string     `json:"type"`
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Body    []acNode   `json:"body"`
	Actions []acAction `json:"actions,omitempty"`
}

// newCard builds the card envelope with a fixed schema/version.
func newCard(body []acNode, actions []acAction) adaptiveCard {
	return adaptiveCard{
		Type:    "AdaptiveCard",
		Schema:  adaptiveCardSchema,
		Version: adaptiveCardVersion,
		Body:    body,
		Actions: actions,
	}
}

// wireCard marshals a card into the teamsWireMessage {text, card} payload the
// outbound path attaches to an Activity, tagging the neutral Card MIME.
func wireCard(fallback string, card adaptiveCard) (channel.Card, error) {
	cardJSON, err := json.Marshal(card)
	if err != nil {
		return channel.Card{}, fmt.Errorf("teams channel: encode adaptive card: %w", err)
	}
	payload, err := json.Marshal(teamsWireMessage{Text: strings.TrimSpace(fallback), Card: cardJSON})
	if err != nil {
		return channel.Card{}, fmt.Errorf("teams channel: encode card payload: %w", err)
	}
	return channel.Card{MIME: teamsCardMIME, Payload: payload}, nil
}

// --- display cards (progress / terminal) ------------------------------------

// RenderProgress renders the in-flight ("executing") card. now defaults to
// time.Now().UTC() when ProgressState.Now is zero so still-running step
// durations advance; callers pin Now for deterministic output.
func (c *Channel) RenderProgress(_ context.Context, _ channel.ReplyTarget, state channel.ProgressState) (channel.Card, error) {
	now := state.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	title := resolveTitle(state.Title)

	body := []acNode{titleBlock(title)}
	body = append(body, stepBlocks(state.Steps, now)...)
	body = append(body, c.streamingBlocks(state.StreamingText)...)

	status := "运行中"
	if state.Done {
		status = "已完成"
	}
	if state.Elapsed > 0 {
		status += " · " + formatDuration(state.Elapsed)
	}
	body = append(body, subtleBlock(status))

	fallback := title + " 运行中"
	if state.Done {
		fallback = title + " 已完成"
	}
	return wireCard(fallback, newCard(body, nil))
}

// RenderTerminal renders the terminal Done / Error card. Success selects the
// Done path; otherwise renderError builds the failure card with the shared
// empty-message fallback.
func (c *Channel) RenderTerminal(_ context.Context, _ channel.ReplyTarget, result channel.TerminalResult) (channel.Card, error) {
	title := resolveTitle(result.Title)
	if !result.Success {
		return c.renderError(title, result)
	}

	body := []acNode{titleBlock(title)}
	body = append(body, stepBlocks(result.Steps, time.Time{})...)
	if think := strings.TrimSpace(result.Thinking); think != "" {
		body = append(body, acNode{Type: "TextBlock", Text: "**思考**", Wrap: true})
		body = append(body, acNode{Type: "TextBlock", Text: c.Format(think), Wrap: true, IsSubtle: true})
	}
	body = append(body, c.streamingBlocks(result.StreamingText)...)
	if result.Usage != nil {
		body = append(body, subtleBlock(usageLine(result.Usage)))
	}

	fallback := firstLine(strings.TrimSpace(result.StreamingText))
	if fallback == "" {
		fallback = title + " 已完成"
	}
	return wireCard(fallback, newCard(body, nil))
}

// renderError builds the failure card. RawError renders as a monospace block;
// RunDetailURL becomes a link line; GuestHint is Format-ed.
func (c *Channel) renderError(title string, result channel.TerminalResult) (channel.Card, error) {
	msg := strings.TrimSpace(result.ErrorMessage)
	if msg == "" {
		msg = errCardFallbackMessage
	}
	body := []acNode{
		titleBlock(title),
		{Type: "TextBlock", Text: "**" + msg + "**", Wrap: true, Color: "Attention"},
	}
	if raw := strings.TrimSpace(result.RawError); raw != "" {
		body = append(body, acNode{Type: "TextBlock", Text: raw, Wrap: true, FontType: "Monospace", IsSubtle: true})
	}
	if url := strings.TrimSpace(result.RunDetailURL); url != "" {
		body = append(body, acNode{Type: "TextBlock", Text: "[查看运行详情](" + url + ")", Wrap: true})
	}
	if hint := strings.TrimSpace(result.GuestHint); hint != "" {
		body = append(body, acNode{Type: "TextBlock", Text: c.Format(hint), Wrap: true})
	}
	return wireCard(title+" — "+msg, newCard(body, nil))
}

// --- interactive cards (permission / choice / credential) -------------------

// RenderPermission renders the Allow/Deny card. Both actions carry the
// permission_request_id in their submit data under the neutral keys HandleAction
// maps to CardActionPermissionAllow / CardActionPermissionDeny.
func (c *Channel) RenderPermission(_ context.Context, _ channel.ReplyTarget, req channel.PermissionRequest) (channel.Card, error) {
	title := resolveTitle(req.Title)
	tool := strings.TrimSpace(req.ToolName)
	if tool == "" {
		tool = "某工具"
	}
	body := []acNode{
		titleBlock(title),
		{Type: "TextBlock", Text: "**" + tool + "** 需要你的批准才能运行。", Wrap: true, Color: "Warning"},
	}
	if input := strings.TrimSpace(req.ToolInput); input != "" {
		body = append(body, acNode{Type: "TextBlock", Text: input, Wrap: true, FontType: "Monospace", IsSubtle: true})
	}
	actions := []acAction{
		{
			Type:  "Action.Submit",
			Title: "允许",
			Style: "positive",
			Data:  map[string]any{"action": "permission_allow", "permission_request_id": req.RequestID},
		},
		{
			Type:  "Action.Submit",
			Title: "拒绝",
			Style: "destructive",
			Data:  map[string]any{"action": "permission_deny", "permission_request_id": req.RequestID},
		},
	}
	return wireCard(title+" — 是否允许 "+tool+"?", newCard(body, actions))
}

// RenderChoiceForm renders the prompt_for_user_choice card: one Input.ChoiceSet
// per question (id "q<idx>", isMultiSelect when MultiSelect) plus a single
// Submit carrying the request id. On submit each ChoiceSet's value merges into
// the activity value under its id, so HandleAction reads FormValues["q<idx>"].
func (c *Channel) RenderChoiceForm(_ context.Context, _ channel.ReplyTarget, form channel.ChoiceForm) (channel.Card, error) {
	title := resolveTitle(form.Title)
	body := []acNode{titleBlock(title)}

	for i, q := range form.Questions {
		prompt := strings.TrimSpace(q.Question)
		if prompt == "" {
			prompt = strings.TrimSpace(q.Header)
		}
		body = append(body, acNode{Type: "TextBlock", Text: "**" + prompt + "**", Wrap: true})

		choices := make([]acChoice, 0, len(q.Options))
		for _, label := range q.Options {
			choices = append(choices, acChoice{Title: label, Value: label})
		}
		body = append(body, acNode{
			Type:          "Input.ChoiceSet",
			ID:            fmt.Sprintf("q%d", i),
			IsMultiSelect: q.MultiSelect,
			ChoiceStyle:   "expanded",
			Choices:       choices,
		})
	}

	actions := []acAction{{
		Type:  "Action.Submit",
		Title: "提交",
		Style: "positive",
		Data:  map[string]any{"action": "ask_user_choice_submit", "request_id": form.RequestID},
	}}
	return wireCard(title+" 需要你的选择", newCard(body, actions))
}

// RenderCredentialForm renders the missing-credential form: one Input.Text per
// field (id "credential_<kind>" so the submitted value lands as
// FormValues["credential_<kind>"]) plus a Submit carrying the minted qkey.
func (c *Channel) RenderCredentialForm(_ context.Context, _ channel.ReplyTarget, form channel.CredentialForm) (channel.Card, error) {
	title := resolveTitle(form.Title)
	body := []acNode{
		titleBlock(title),
		{Type: "TextBlock", Text: "本次运行需要先填写凭据。", Wrap: true},
	}

	for i, f := range form.Fields {
		label := strings.TrimSpace(f.Label)
		if label == "" {
			label = strings.TrimSpace(f.CapabilityName)
		}
		if label == "" {
			label = fmt.Sprintf("字段 %d", i+1)
		}
		kind := strings.TrimSpace(f.Kind)
		if kind == "" {
			kind = fmt.Sprintf("field_%d", i)
		}
		body = append(body, acNode{Type: "TextBlock", Text: "**" + label + "**", Wrap: true})
		body = append(body, acNode{
			Type:        "Input.Text",
			ID:          "credential_" + kind,
			Placeholder: strings.TrimSpace(f.Placeholder),
		})
	}

	actions := []acAction{{
		Type:  "Action.Submit",
		Title: "提交",
		Style: "positive",
		Data:  map[string]any{"action": "credential_form_submit", "qkey": form.Qkey},
	}}
	return wireCard(title+" 需要凭据", newCard(body, actions))
}

// renderActionResultCard renders the neutral post-click ActionResultCard into a
// display-only Adaptive Card — the Teams twin of the Slack/Feishu inline result
// cards. Pure function of its input so HandleAction can marshal it into an ack.
func renderActionResultCard(result *channel.ActionResultCard) (string, adaptiveCard) {
	title := resolveTitle(result.Title)
	body := []acNode{titleBlock(title)}
	fallback := title

	switch result.Kind {
	case channel.CardActionPermissionAllow, channel.CardActionPermissionDeny:
		if result.Approved {
			body = append(body, acNode{Type: "TextBlock", Text: "**已允许**", Wrap: true, Color: "Good"})
			fallback = title + " — 已允许"
		} else {
			body = append(body, acNode{Type: "TextBlock", Text: "**已拒绝**", Wrap: true, Color: "Attention"})
			fallback = title + " — 已拒绝"
		}
	case channel.CardActionCredentialSubmit:
		if result.Rejected {
			reason := strings.TrimSpace(result.RejectReason)
			if reason == "" {
				reason = "提交被拒绝。"
			}
			body = append(body, acNode{Type: "TextBlock", Text: "**" + reason + "**", Wrap: true, Color: "Attention"})
			fallback = title + " — " + reason
		} else {
			summary := strings.TrimSpace(result.Summary)
			if summary == "" {
				summary = "凭据已保存。"
			}
			body = append(body, acNode{Type: "TextBlock", Text: summary, Wrap: true, Color: "Good"})
			fallback = title + " — " + summary
		}
	default:
		if summary := strings.TrimSpace(result.Summary); summary != "" {
			body = append(body, acNode{Type: "TextBlock", Text: summary, Wrap: true})
			fallback = title + " — " + summary
		}
	}
	return fallback, newCard(body, nil)
}

// --- small builders / helpers -----------------------------------------------

// titleBlock is the bold large header TextBlock every card opens with.
func titleBlock(title string) acNode {
	return acNode{Type: "TextBlock", Text: title, Weight: "Bolder", Size: "Large", Wrap: true}
}

// subtleBlock is a muted status/usage line.
func subtleBlock(text string) acNode {
	return acNode{Type: "TextBlock", Text: text, Wrap: true, IsSubtle: true, Size: "Small"}
}

// stepBlocks renders the folded tool steps as one muted multi-line TextBlock
// (Teams has no per-element context row, so the lines are joined with newlines).
func stepBlocks(steps []gateway.StepInfo, now time.Time) []acNode {
	if len(steps) == 0 {
		return nil
	}
	lines := make([]string, 0, len(steps))
	for _, s := range steps {
		lines = append(lines, stepLine(s, now))
	}
	return []acNode{subtleBlock(strings.Join(lines, "\n\n"))}
}

// stepLine renders one step "· Label · 12s"; duration is suppressed when zero.
func stepLine(s gateway.StepInfo, now time.Time) string {
	label := strings.TrimSpace(s.Label)
	if label == "" {
		label = strings.TrimSpace(s.Tool)
	}
	line := "· " + label
	if d := s.Duration(now); d > 0 {
		line += " · " + formatDuration(d)
	}
	return line
}

// streamingBlocks Format-s the assistant text and splits it into TextBlocks
// each within the per-message budget. Empty text yields no blocks.
func (c *Channel) streamingBlocks(text string) []acNode {
	t := strings.TrimSpace(text)
	if t == "" {
		return nil
	}
	chunks := c.Truncate(c.Format(t))
	blocks := make([]acNode, 0, len(chunks))
	for _, ch := range chunks {
		blocks = append(blocks, acNode{Type: "TextBlock", Text: ch, Wrap: true})
	}
	return blocks
}

func resolveTitle(title string) string {
	if t := strings.TrimSpace(title); t != "" {
		return t
	}
	return defaultCardTitle
}

// usageLine renders the model-usage footer as a "·"-joined line.
func usageLine(u *gateway.UsageStats) string {
	parts := make([]string, 0, 3)
	if u.CostUSD > 0 {
		parts = append(parts, fmt.Sprintf("$%.2f", u.CostUSD))
	}
	if u.ContextWindow > 0 {
		parts = append(parts, fmt.Sprintf("%d/%d tokens", u.ContextUsed, u.ContextWindow))
	}
	if m := strings.TrimSpace(u.Model); m != "" {
		parts = append(parts, m)
	}
	if len(parts) == 0 {
		return "用量不可用"
	}
	return strings.Join(parts, " · ")
}

// formatDuration renders a wall-clock duration as "12s" / "1m30s".
func formatDuration(d time.Duration) string {
	secs := int(d.Round(time.Second) / time.Second)
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	return fmt.Sprintf("%dm%ds", secs/60, secs%60)
}

// firstLine returns the first line of s (a notification fallback).
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}
