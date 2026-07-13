// Package slack — mrkdwn text codec (PR #4a).
//
// Slack's markup ("mrkdwn") is not Markdown: bold is a single *asterisk*,
// links are <url|text>, and there is no heading syntax. The neutral driver
// delegates per-platform formatting + length-bounded splitting to the adapter
// via the optional channel.TextCodec interface; *Channel implements it here so
// the Block Kit renderers (blockkit.go) and the future outbound path share one
// conversion. Conversions skip fenced code blocks so code is never mangled.
package slack

import (
	"regexp"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

var (
	// mdBoldRe matches Markdown **bold** (non-greedy) → Slack *bold*.
	mdBoldRe = regexp.MustCompile(`\*\*(.+?)\*\*`)
	// mdStrikeRe matches Markdown ~~strike~~ → Slack ~strike~.
	mdStrikeRe = regexp.MustCompile(`~~(.+?)~~`)
	// mdLinkRe matches Markdown [text](url) → Slack <url|text>.
	mdLinkRe = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	// mdHeadingRe matches an ATX heading line (# .. ###### ) → bold line.
	mdHeadingRe = regexp.MustCompile(`^\s{0,3}(#{1,6})\s+(.*)$`)
	// mdImageRe matches a Markdown image ![alt](url); ExtractMedia pulls these.
	mdImageRe = regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+)\)`)
)

// Format converts neutral Markdown markup to Slack mrkdwn. It works line by
// line and leaves fenced code blocks untouched. Headings become bold lines;
// links, bold and strikethrough are rewritten to their mrkdwn equivalents.
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
		lines[i] = formatInline(line)
	}
	return strings.Join(lines, "\n")
}

// formatInline rewrites a single non-code line. Links are converted before
// bold so emphasis inside a link label is still rewritten.
func formatInline(line string) string {
	if m := mdHeadingRe.FindStringSubmatch(line); m != nil {
		line = "*" + strings.TrimSpace(m[2]) + "*"
	}
	line = mdLinkRe.ReplaceAllString(line, "<$2|$1>")
	line = mdBoldRe.ReplaceAllString(line, "*$1*")
	line = mdStrikeRe.ReplaceAllString(line, "~$1~")
	return line
}

// Truncate splits text into chunks within Slack's per-message budget,
// preserving fenced code blocks: when a split falls inside a fence the chunk
// is closed with a fence and the next chunk reopened with one, so neither
// half renders as broken code.
func (c *Channel) Truncate(text string) []string {
	return channel.SplitPreservingFences(text, slackMaxMessageLen)
}

// ExtractMedia pulls Markdown image references (![alt](url)) out of the text,
// returning them as neutral Media plus the remaining text with the image
// syntax removed. Non-image links are left in place for Format to rewrite.
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

// escapeMrkdwn escapes the three characters Slack treats specially in mrkdwn
// text so literal user/error strings render verbatim. Applied only to plain
// inserts (error copy, step labels, model name) — never to Format output,
// which intentionally emits <url|text> link syntax.
func escapeMrkdwn(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
