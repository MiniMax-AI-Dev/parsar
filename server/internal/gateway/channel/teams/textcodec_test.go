package teams

import (
	"testing"

	channeltest "github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel/testutil"
)

func TestTextCodec(t *testing.T) {
	channeltest.RunTextCodecContract(t, channeltest.TextCodecContract{
		Codec:    newTestChannel(),
		MaxRunes: teamsMaxMessageLen,
		FormatCases: []channeltest.FormatCase{
			{Name: "heading", Text: "# Title", Want: "**Title**"},
			{Name: "subheading", Text: "## Sub", Want: "**Sub**"},
			{Name: "markdown passthrough", Text: "plain **bold** line", Want: "plain **bold** line"},
			{Name: "fenced code", Text: "```\n# not a heading\n```", Want: "```\n# not a heading\n```"},
		},
	})
}
