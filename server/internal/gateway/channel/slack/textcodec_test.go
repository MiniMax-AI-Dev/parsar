package slack

import (
	"testing"

	channeltest "github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel/testutil"
)

func TestTextCodec(t *testing.T) {
	channeltest.RunTextCodecContract(t, channeltest.TextCodecContract{
		Codec:    newTestChannel(),
		MaxRunes: slackMaxMessageLen,
		FormatCases: []channeltest.FormatCase{
			{Name: "bold", Text: "**bold**", Want: "*bold*"},
			{Name: "strike", Text: "~~strike~~", Want: "~strike~"},
			{Name: "link", Text: "[t](http://x)", Want: "<http://x|t>"},
			{Name: "heading", Text: "# Head", Want: "*Head*"},
			{Name: "subheading", Text: "## Sub", Want: "*Sub*"},
			{Name: "mixed", Text: "see [docs](http://d) and **bold**", Want: "see <http://d|docs> and *bold*"},
			{Name: "formatted link label", Text: "[**t**](http://u)", Want: "<http://u|*t*>"},
			{Name: "plain", Text: "plain text", Want: "plain text"},
			{
				Name: "fenced code",
				Text: "before **b**\n```\n**not bold** [no](link)\n```\nafter [t](http://u)",
				Want: "before *b*\n```\n**not bold** [no](link)\n```\nafter <http://u|t>",
			},
		},
	})
}
