// Package teams — Markdown text codec.
//
// Unlike Slack's mrkdwn, a Teams Adaptive Card TextBlock renders a subset of
// real Markdown (bold **, italics, links [t](u), bullet lists), so Format is a
// light normalization rather than a dialect translation: it only rewrites ATX
// headings (unsupported by a TextBlock) into bold lines and leaves the rest
// intact. The neutral driver delegates per-platform formatting + length-bounded
// splitting to the adapter via the optional channel.TextCodec interface;
// *Channel implements it here so the Adaptive Card renderers (adaptivecard.go)
// and the outbound path share one conversion. Conversions skip fenced code
// blocks so code is never mangled.
package teams

import (
	"regexp"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

var (
	// mdHeadingRe matches an ATX heading line (# .. ###### ) → bold line, since
	// a TextBlock has no heading syntax.
	mdHeadingRe = regexp.MustCompile(`^\s{0,3}(#{1,6})\s+(.*)$`)
	// mdImageRe matches a Markdown image ![alt](url); ExtractMedia pulls these
	// out so they ride as native attachments rather than broken inline syntax.
	mdImageRe = regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+)\)`)
)

// Format normalizes neutral Markdown to the Teams TextBlock subset. It works
// line by line and leaves fenced code blocks untouched; only headings are
// rewritten (to bold), since bold/italics/links/lists already render.
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

// Truncate splits text into chunks within Teams' per-message budget, preserving
// fenced code blocks so neither half of a split renders as broken code.
func (c *Channel) Truncate(text string) []string {
	return channel.SplitPreservingFences(text, teamsMaxMessageLen)
}

// ExtractMedia pulls Markdown image references (![alt](url)) out of the text,
// returning them as neutral Media plus the remaining text with the image syntax
// removed. Non-image links are left in place for a TextBlock to render.
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
