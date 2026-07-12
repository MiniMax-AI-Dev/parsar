// Package discord — Markdown text codec (PR #5a).
//
// Unlike Slack mrkdwn, Discord renders standard Markdown (**bold**, *italic*,
// `code`, fenced ```blocks```, [text](url) links), so Format is largely a
// pass-through — it only strips ATX heading markers Discord does not render as
// headings, rewriting them to bold lines for visual parity with the Slack
// adapter. The neutral driver delegates per-platform formatting + length-
// bounded splitting to the adapter via the optional channel.TextCodec
// interface; *Channel implements it here so the Embed renderers (embed.go) and
// the future outbound path (5b) share one conversion. Conversions skip fenced
// code blocks so code is never mangled.
package discord

import (
	"regexp"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

var (
	// mdHeadingRe matches an ATX heading line (# .. ###### ); Discord renders
	// #/##/### as headings already, but to keep card bodies compact (the
	// renderers inline these into embed fields/descriptions) we normalize them
	// to bold lines, matching the Slack adapter's behaviour.
	mdHeadingRe = regexp.MustCompile(`^\s{0,3}(#{1,6})\s+(.*)$`)
	// mdImageRe matches a Markdown image ![alt](url); ExtractMedia pulls these.
	mdImageRe = regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+)\)`)
)

// Format normalizes neutral Markdown for Discord. Discord is Markdown-native,
// so the only rewrite is folding ATX headings to bold lines; everything else
// (bold, italic, links, strikethrough) is already Discord syntax and passes
// through untouched. Fenced code blocks are left verbatim.
func (c *Channel) Format(text string) string {
	lines := strings.Split(text, "\n")
	inFence := false
	for i, line := range lines {
		if isFence(line) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if m := mdHeadingRe.FindStringSubmatch(line); m != nil {
			lines[i] = "**" + strings.TrimSpace(m[2]) + "**"
		}
	}
	return strings.Join(lines, "\n")
}

// Truncate splits text into chunks within Discord's 2000-char message budget,
// preserving fenced code blocks: when a split falls inside a fence the chunk is
// closed with a fence and the next reopened with one, so neither half renders
// as broken code.
func (c *Channel) Truncate(text string) []string {
	return channel.SplitPreservingFences(text, discordMaxMessageLen)
}

// ExtractMedia pulls Markdown image references (![alt](url)) out of the text,
// returning them as neutral Media plus the remaining text with the image
// syntax removed. Non-image links are left in place (Discord renders them).
func (c *Channel) ExtractMedia(text string) ([]channel.Media, string) {
	idx := mdImageRe.FindAllStringSubmatchIndex(text, -1)
	if len(idx) == 0 {
		return nil, text
	}
	media := make([]channel.Media, 0, len(idx))
	var b strings.Builder
	last := 0
	for _, m := range idx {
		b.WriteString(text[last:m[0]])
		url := strings.TrimSpace(text[m[4]:m[5]])
		media = append(media, channel.Media{Kind: "image", URL: url})
		last = m[1]
	}
	b.WriteString(text[last:])
	return media, strings.TrimSpace(b.String())
}

// isFence reports whether a line opens or closes a Markdown code fence.
func isFence(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "```")
}
