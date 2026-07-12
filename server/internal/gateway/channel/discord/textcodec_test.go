package discord

import (
	"testing"

	channeltest "github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel/testutil"
)

func TestTextCodec(t *testing.T) {
	channeltest.RunTextCodecContract(t, channeltest.TextCodecContract{
		Codec:    newTestChannel(),
		MaxRunes: discordMaxMessageLen,
		FormatCases: []channeltest.FormatCase{
			{Name: "bold", Text: "**bold**", Want: "**bold**"},
			{Name: "italic", Text: "*italic*", Want: "*italic*"},
			{Name: "strike", Text: "~~strike~~", Want: "~~strike~~"},
			{Name: "link", Text: "[t](http://x)", Want: "[t](http://x)"},
			{Name: "inline code", Text: "`code`", Want: "`code`"},
			{Name: "heading", Text: "# Head", Want: "**Head**"},
			{Name: "subheading", Text: "### Sub", Want: "**Sub**"},
			{Name: "mixed", Text: "see [docs](http://d) and **bold**", Want: "see [docs](http://d) and **bold**"},
			{Name: "plain", Text: "plain text", Want: "plain text"},
			{Name: "fenced code", Text: "before\n```\n# not a heading\n```\nafter", Want: "before\n```\n# not a heading\n```\nafter"},
		},
	})
}
