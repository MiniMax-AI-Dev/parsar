// Package discord — outbound Embed + message-component rendering (PR #5a).
//
// RenderProgress / RenderTerminal / RenderPermission / RenderChoiceForm /
// RenderCredentialForm turn the neutral channel.ProgressState /
// channel.TerminalResult / … the driver computes into a Discord message payload
// ({content, embeds, components}). They are pure functions of their input (the
// only wall-clock read is the RenderProgress "now" default, which the caller
// pins for deterministic output), so golden tests lock the JSON.
//
// Discord has no prior renderer, so — like the Slack Block Kit builders — these
// are net-new. They honor Discord's structural limits: embed title ≤256, embed
// description ≤4096, ≤25 fields (field.name ≤256, field.value ≤1024), footer
// ≤2048, and ≤5 action rows per message (each row ≤5 buttons, or exactly one
// select). Streaming text is Markdown-normalized via the TextCodec
// (textcodec.go) before length-capping.
//
// Interactive components carry their round-trip id in a ≤100-char custom_id
// (Discord has no separate button "value"): the neutral action id and its value
// are packed as "<action>:<value>" (customID); HandleAction (5c) splits them
// back, mapping the action half to the same neutral CardActionKind the Slack and
// Feishu adapters use so the downstream router never sees a platform.
package discord

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// discordCardMIME tags a rendered embed/component payload. The driver treats
// Card.Payload opaquely; the MIME lets the multi-platform send path assert it is
// handing the right card shape to the right channel.
const discordCardMIME = "discord/embed"

// Discord structural limits.
const (
	discordTitleLimit       = 256
	discordDescLimit        = 4096
	discordFieldValueLimit  = 1024
	discordFooterLimit      = 2048
	discordMaxActionRows    = 5
	discordCustomIDLimit    = 100
	discordSelectMaxOptions = 25
)

// Discord embed colors (decimal RGB) selecting the card's left bar by state.
const (
	colorRunning    = 0x5865F2 // blurple — in-flight
	colorDone       = 0x57F287 // green — success
	colorError      = 0xED4245 // red — failure
	colorPermission = 0xFEE75C // yellow — awaiting approval
	colorForm       = 0x5865F2 // blurple — input requested
)

// Discord message-component types and button styles.
const (
	componentActionRow    = 1
	componentButton       = 2
	componentStringSelect = 3

	buttonPrimary   = 1 // blurple
	buttonSecondary = 2 // grey
	buttonSuccess   = 3 // green
	buttonDanger    = 4 // red
)

// defaultCardTitle mirrors the Feishu/Slack adapters' empty-title fallback so a
// run with no agent name still renders an embed title.
const defaultCardTitle = "Agent"

// errCardFallbackMessage mirrors the Feishu/Slack adapters' failed-run fallback
// copy so a TerminalResult with an empty ErrorMessage renders the same
// user-facing failure text across platforms.
const errCardFallbackMessage = "Agent 运行失败，请稍后重试。"

// ackReceived mirrors Feishu's/Slack's default toast so an unrouted click looks
// the same to the user across platforms (consumed by action.go in 5c).
const ackReceived = "操作已收到"

// --- Discord wire structs (typed for deterministic JSON key order) ----------

type deEmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

type deEmbedFooter struct {
	Text string `json:"text"`
}

type deEmbed struct {
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description,omitempty"`
	Color       int            `json:"color,omitempty"`
	Fields      []deEmbedField `json:"fields,omitempty"`
	Footer      *deEmbedFooter `json:"footer,omitempty"`
}

// deButton is a message-component button. Style picks the colour; CustomID is
// the packed "<action>:<value>" round-trip id HandleAction decodes.
type deButton struct {
	Type     int    `json:"type"` // componentButton
	Style    int    `json:"style"`
	Label    string `json:"label"`
	CustomID string `json:"custom_id"`
}

// deSelectOption is one string_select choice.
type deSelectOption struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// deSelect is a string_select menu (one per action row). MinValues/MaxValues
// gate single vs multi pick.
type deSelect struct {
	Type        int              `json:"type"` // componentStringSelect
	CustomID    string           `json:"custom_id"`
	Placeholder string           `json:"placeholder,omitempty"`
	MinValues   int              `json:"min_values"`
	MaxValues   int              `json:"max_values"`
	Options     []deSelectOption `json:"options"`
}

// deActionRow holds buttons (≤5) or exactly one select. Components is any-typed
// because a row is heterogeneous across card kinds.
type deActionRow struct {
	Type       int   `json:"type"` // componentActionRow
	Components []any `json:"components"`
}

// deMessage is the rendered Discord message payload.
type deMessage struct {
	Content    string        `json:"content,omitempty"`
	Embeds     []deEmbed     `json:"embeds,omitempty"`
	Components []deActionRow `json:"components,omitempty"`
}

// toolEmoji maps a canonical tool name to a Unicode emoji for the step line.
// Discord renders Unicode emoji natively (no :shortcode: like Slack). Tools
// absent from the table fall back to defaultToolEmoji.
var toolEmoji = map[string]string{
	"Bash":      "💻",
	"Read":      "📄",
	"Edit":      "✏️",
	"Write":     "✏️",
	"Grep":      "🔍",
	"Glob":      "🔍",
	"WebFetch":  "🔗",
	"WebSearch": "🔍",
	"LSP":       "💻",
	"Skill":     "🤖",
	"Agent":     "💻",
	"Workflow":  "🤖",
}

const defaultToolEmoji = "⚙️"

func emojiForTool(tool string) string {
	if e, ok := toolEmoji[strings.TrimSpace(tool)]; ok {
		return e
	}
	return defaultToolEmoji
}

// RenderProgress renders the in-flight ("executing") embed. now defaults to
// time.Now().UTC() when ProgressState.Now is zero (matching the Feishu/Slack
// adapters), so still-running step durations advance; callers pin Now for
// deterministic output.
func (c *Channel) RenderProgress(_ context.Context, _ channel.ReplyTarget, state channel.ProgressState) (channel.Card, error) {
	now := state.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	title := resolveTitle(state.Title)

	desc := buildBody(stepLines(state.Steps, now), c.Format(state.StreamingText))

	status := "⏳ Running"
	if state.Done {
		status = "✅ Done"
	}
	if state.Elapsed > 0 {
		status += " · " + formatDuration(state.Elapsed)
	}
	color := colorRunning
	if state.Done {
		color = colorDone
	}

	embed := deEmbed{
		Title:       truncateRunes(title, discordTitleLimit),
		Description: truncateRunes(desc, discordDescLimit),
		Color:       color,
		Footer:      &deEmbedFooter{Text: truncateRunes(status, discordFooterLimit)},
	}
	fallback := title + " is running"
	if state.Done {
		fallback = title + " finished"
	}
	return cardFrom(fallback, []deEmbed{embed}, nil)
}

// RenderTerminal renders the terminal Done / Error embed. Success selects the
// Done path; otherwise renderError builds the failure embed with the same
// empty-message fallback the Feishu/Slack adapters apply.
func (c *Channel) RenderTerminal(_ context.Context, _ channel.ReplyTarget, result channel.TerminalResult) (channel.Card, error) {
	title := resolveTitle(result.Title)
	if !result.Success {
		return c.renderError(title, result)
	}

	// Terminal steps have ended, so a zero clock yields their final duration
	// deterministically (StepInfo.Duration only reaches for `now` on a
	// still-running step, which a terminal card has none of).
	var sections []string
	if steps := stepLines(result.Steps, time.Time{}); steps != "" {
		sections = append(sections, steps)
	}
	if think := strings.TrimSpace(result.Thinking); think != "" {
		sections = append(sections, "**Thinking**\n"+quote(c.Format(think)))
	}
	if body := strings.TrimSpace(c.Format(result.StreamingText)); body != "" {
		sections = append(sections, body)
	}
	desc := strings.Join(sections, "\n\n")

	embed := deEmbed{
		Title:       truncateRunes(title, discordTitleLimit),
		Description: truncateRunes(desc, discordDescLimit),
		Color:       colorDone,
	}
	if result.Usage != nil {
		embed.Footer = &deEmbedFooter{Text: truncateRunes(usageLine(result.Usage), discordFooterLimit)}
	}

	fallback := firstLine(strings.TrimSpace(result.StreamingText))
	if fallback == "" {
		fallback = title + " finished"
	}
	return cardFrom(fallback, []deEmbed{embed}, nil)
}

// renderError builds the failure embed. RawError is rendered in a code block;
// RunDetailURL becomes a footer-equivalent link line; GuestHint is appended.
func (c *Channel) renderError(title string, result channel.TerminalResult) (channel.Card, error) {
	msg := strings.TrimSpace(result.ErrorMessage)
	if msg == "" {
		msg = errCardFallbackMessage
	}
	var sections []string
	sections = append(sections, "❌ **"+msg+"**")
	if raw := strings.TrimSpace(result.RawError); raw != "" {
		sections = append(sections, codeBlock(raw))
	}
	if url := strings.TrimSpace(result.RunDetailURL); url != "" {
		sections = append(sections, "[View run details]("+url+")")
	}
	if hint := strings.TrimSpace(result.GuestHint); hint != "" {
		sections = append(sections, c.Format(hint))
	}

	embed := deEmbed{
		Title:       truncateRunes(title, discordTitleLimit),
		Description: truncateRunes(strings.Join(sections, "\n\n"), discordDescLimit),
		Color:       colorError,
	}
	return cardFrom(title+" — "+msg, []deEmbed{embed}, nil)
}

// --- interactive cards (permission / choice / credential) -------------------

// RenderPermission renders the Allow/Deny card. The two buttons carry the
// permission_request_id packed into their custom_id under the neutral action
// ids permission_allow / permission_deny that HandleAction maps to
// CardActionPermissionAllow / CardActionPermissionDeny.
func (c *Channel) RenderPermission(_ context.Context, _ channel.ReplyTarget, req channel.PermissionRequest) (channel.Card, error) {
	title := resolveTitle(req.Title)
	tool := strings.TrimSpace(req.ToolName)
	if tool == "" {
		tool = "a tool"
	}

	sections := []string{"⚠️ **" + tool + "** needs your approval to run."}
	if input := strings.TrimSpace(req.ToolInput); input != "" {
		sections = append(sections, codeBlock(input))
	}
	embed := deEmbed{
		Title:       truncateRunes(title, discordTitleLimit),
		Description: truncateRunes(strings.Join(sections, "\n\n"), discordDescLimit),
		Color:       colorPermission,
	}
	row := deActionRow{
		Type: componentActionRow,
		Components: []any{
			deButton{Type: componentButton, Style: buttonSuccess, Label: "Allow", CustomID: customID("permission_allow", req.RequestID)},
			deButton{Type: componentButton, Style: buttonDanger, Label: "Deny", CustomID: customID("permission_deny", req.RequestID)},
		},
	}
	return cardFrom(title+" — approve "+tool+"?", []deEmbed{embed}, []deActionRow{row})
}

// RenderChoiceForm renders the prompt_for_user_choice card: one string select
// per question (multi when MultiSelect) plus a Submit button carrying the
// request id. Each select's custom_id is ask_user_choice_pick:<idx> so the
// inbound decode can fold its picks by question index; the Submit button uses
// ask_user_choice_submit.
//
// Discord caps a message at 5 action rows. One row is reserved for Submit, so
// at most 4 question selects render; any beyond that are dropped with a note
// appended to the embed (a multi-question form rarely exceeds 4 in practice).
func (c *Channel) RenderChoiceForm(_ context.Context, _ channel.ReplyTarget, form channel.ChoiceForm) (channel.Card, error) {
	title := resolveTitle(form.Title)

	maxSelects := discordMaxActionRows - 1 // reserve one row for Submit
	questions := form.Questions
	dropped := 0
	if len(questions) > maxSelects {
		dropped = len(questions) - maxSelects
		questions = questions[:maxSelects]
	}

	var rows []deActionRow
	var prompts []string
	for i, q := range questions {
		prompt := strings.TrimSpace(q.Question)
		if prompt == "" {
			prompt = strings.TrimSpace(q.Header)
		}
		prompts = append(prompts, fmt.Sprintf("**%d.** %s", i+1, prompt))

		opts := make([]deSelectOption, 0, len(q.Options))
		for _, label := range q.Options {
			opts = append(opts, deSelectOption{Label: truncateRunes(label, discordTitleLimit), Value: label})
		}
		if len(opts) > discordSelectMaxOptions {
			opts = opts[:discordSelectMaxOptions]
		}
		maxValues := 1
		if q.MultiSelect {
			maxValues = len(opts)
		}
		rows = append(rows, deActionRow{
			Type: componentActionRow,
			Components: []any{deSelect{
				Type:        componentStringSelect,
				CustomID:    customID("ask_user_choice_pick", fmt.Sprintf("%d", i)),
				Placeholder: "Select…",
				MinValues:   0,
				MaxValues:   maxValues,
				Options:     opts,
			}},
		})
	}

	rows = append(rows, deActionRow{
		Type: componentActionRow,
		Components: []any{deButton{
			Type: componentButton, Style: buttonPrimary, Label: "Submit",
			CustomID: customID("ask_user_choice_submit", form.RequestID),
		}},
	})

	desc := strings.Join(prompts, "\n")
	if dropped > 0 {
		desc += fmt.Sprintf("\n\n_(%d more question(s) omitted — Discord allows at most %d selects per card)_", dropped, maxSelects)
	}
	embed := deEmbed{
		Title:       truncateRunes(title, discordTitleLimit),
		Description: truncateRunes(desc, discordDescLimit),
		Color:       colorForm,
	}
	return cardFrom(title+" needs your input", []deEmbed{embed}, rows)
}

// RenderCredentialForm renders the missing-credential card. Discord cannot place
// text inputs inside a message (they live only in modals, which open from a
// component interaction), so the card lists the required fields in the embed and
// offers a single Submit button under credential_form_submit:<qkey>. HandleAction
// (5c) opens a modal from that click and collects the values; the qkey is
// round-tripped through the custom_id, matching the neutral action id the Slack
// adapter uses.
func (c *Channel) RenderCredentialForm(_ context.Context, _ channel.ReplyTarget, form channel.CredentialForm) (channel.Card, error) {
	title := resolveTitle(form.Title)

	sections := []string{"🔑 This run needs credentials before it can continue."}
	fields := make([]deEmbedField, 0, len(form.Fields))
	for i, f := range form.Fields {
		label := strings.TrimSpace(f.Label)
		if label == "" {
			label = strings.TrimSpace(f.CapabilityName)
		}
		if label == "" {
			label = fmt.Sprintf("Field %d", i+1)
		}
		value := strings.TrimSpace(f.Placeholder)
		if value == "" {
			value = "—"
		}
		fields = append(fields, deEmbedField{
			Name:  truncateRunes(label, discordTitleLimit),
			Value: truncateRunes(value, discordFieldValueLimit),
		})
	}

	embed := deEmbed{
		Title:       truncateRunes(title, discordTitleLimit),
		Description: truncateRunes(strings.Join(sections, "\n\n"), discordDescLimit),
		Color:       colorForm,
		Fields:      fields,
	}
	row := deActionRow{
		Type: componentActionRow,
		Components: []any{deButton{
			Type: componentButton, Style: buttonPrimary, Label: "Enter credentials",
			CustomID: customID("credential_form_submit", form.Qkey),
		}},
	}
	return cardFrom(title+" needs credentials", []deEmbed{embed}, []deActionRow{row})
}

// renderActionResultCard renders the neutral post-click ActionResultCard
// (permission verdict, credential reject/submitted, user-choice done) into a
// Discord replacement message — the Discord twin of the Feishu manager's inline
// result cards. It is a pure function of its input so the 5c action ack path can
// marshal it into an interaction response. An unrecognised Kind falls back to
// the Summary line (or just the title). Consumed in 5c.
func renderActionResultCard(result *channel.ActionResultCard) deMessage {
	title := resolveTitle(result.Title)
	embed := deEmbed{Title: truncateRunes(title, discordTitleLimit)}

	switch result.Kind {
	case channel.CardActionPermissionAllow, channel.CardActionPermissionDeny:
		if result.Approved {
			embed.Description = "✅ **Approved**"
			embed.Color = colorDone
		} else {
			embed.Description = "❌ **Denied**"
			embed.Color = colorError
		}
	case channel.CardActionCredentialSubmit:
		if result.Rejected {
			reason := strings.TrimSpace(result.RejectReason)
			if reason == "" {
				reason = "Submission rejected."
			}
			embed.Description = "⛔ **" + reason + "**"
			embed.Color = colorError
		} else {
			summary := strings.TrimSpace(result.Summary)
			if summary == "" {
				summary = "Credentials saved."
			}
			embed.Description = "✅ " + summary
			embed.Color = colorDone
		}
	default:
		if summary := strings.TrimSpace(result.Summary); summary != "" {
			embed.Description = summary
			embed.Color = colorDone
		}
	}
	embed.Description = truncateRunes(embed.Description, discordDescLimit)
	return deMessage{Embeds: []deEmbed{embed}}
}

// --- builders / helpers ------------------------------------------------------

// customID packs a neutral action id and its round-trip value into a Discord
// component custom_id as "<action>:<value>", capped at the 100-char limit.
// HandleAction (5c) splits on the first ':' to recover both halves.
func customID(action, value string) string {
	id := action
	if value != "" {
		id = action + ":" + value
	}
	return truncateRunesPlain(id, discordCustomIDLimit)
}

// buildBody joins the folded step lines and the formatted streaming text into an
// embed description, separated by a blank line when both are present.
func buildBody(steps, streaming string) string {
	var parts []string
	if s := strings.TrimSpace(steps); s != "" {
		parts = append(parts, s)
	}
	if s := strings.TrimSpace(streaming); s != "" {
		parts = append(parts, s)
	}
	return strings.Join(parts, "\n\n")
}

// stepLines renders the folded tool steps as newline-joined lines (Discord has
// no Slack-style context block, so steps fold into the embed description).
func stepLines(steps []gateway.StepInfo, now time.Time) string {
	if len(steps) == 0 {
		return ""
	}
	lines := make([]string, 0, len(steps))
	for _, s := range steps {
		label := strings.TrimSpace(s.Label)
		if label == "" {
			label = strings.TrimSpace(s.Tool)
		}
		line := emojiForTool(s.Tool) + " " + label
		if d := s.Duration(now); d > 0 {
			line += " · " + formatDuration(d)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// cardFrom marshals a content fallback plus embeds/components into a Discord
// message payload, enforcing the 5-action-row cap.
func cardFrom(fallback string, embeds []deEmbed, components []deActionRow) (channel.Card, error) {
	if len(components) > discordMaxActionRows {
		components = components[:discordMaxActionRows]
	}
	payload, err := json.Marshal(deMessage{
		Content:    truncateRunes(fallback, discordMaxMessageLen),
		Embeds:     embeds,
		Components: components,
	})
	if err != nil {
		return channel.Card{}, err
	}
	return channel.Card{MIME: discordCardMIME, Payload: payload}, nil
}

func resolveTitle(title string) string {
	if t := strings.TrimSpace(title); t != "" {
		return t
	}
	return defaultCardTitle
}

func usageLine(u *gateway.UsageStats) string {
	parts := make([]string, 0, 3)
	if u.CostUSD > 0 {
		parts = append(parts, fmt.Sprintf("💰 $%.2f", u.CostUSD))
	}
	if u.ContextWindow > 0 {
		parts = append(parts, fmt.Sprintf("📊 %d/%d tokens", u.ContextUsed, u.ContextWindow))
	}
	if m := strings.TrimSpace(u.Model); m != "" {
		parts = append(parts, m)
	}
	if len(parts) == 0 {
		return "ℹ️ usage unavailable"
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

// codeBlock wraps text in a Markdown code fence, truncated to fit an embed
// description.
func codeBlock(s string) string {
	const fence = "```"
	body := truncateRunesPlain(s, discordDescLimit-2*len(fence)-2)
	return fence + "\n" + body + "\n" + fence
}

// quote prefixes each line with Markdown's blockquote marker.
func quote(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = "> " + l
	}
	return strings.Join(lines, "\n")
}

// firstLine returns the first line of s (used as a content fallback).
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

// truncateRunesPlain cuts s to at most max runes with no ellipsis — for
// machine-read strings (custom_id, code-block bodies) where an appended "…"
// would corrupt a round-trip id or code.
func truncateRunesPlain(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
