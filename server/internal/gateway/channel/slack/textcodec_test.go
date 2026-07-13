package slack

import (
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

func TestFormat_MarkdownToMrkdwn(t *testing.T) {
	c := newTestChannel()
	cases := []struct{ in, want string }{
		{"**bold**", "*bold*"},
		{"~~strike~~", "~strike~"},
		{"[t](http://x)", "<http://x|t>"},
		{"# Head", "*Head*"},
		{"## Sub", "*Sub*"},
		{"see [docs](http://d) and **bold**", "see <http://d|docs> and *bold*"},
		{"[**t**](http://u)", "<http://u|*t*>"}, // link converted before bold
		{"plain text", "plain text"},
	}
	for _, tc := range cases {
		if got := c.Format(tc.in); got != tc.want {
			t.Errorf("Format(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFormat_LeavesFencedCodeUntouched(t *testing.T) {
	c := newTestChannel()
	in := "before **b**\n```\n**not bold** [no](link)\n```\nafter [t](http://u)"
	want := "before *b*\n```\n**not bold** [no](link)\n```\nafter <http://u|t>"
	if got := c.Format(in); got != want {
		t.Errorf("Format with code fence\n got: %q\nwant: %q", got, want)
	}
}

func TestExtractMedia(t *testing.T) {
	c := newTestChannel()
	media, rest := c.ExtractMedia("a ![alt](http://img/1.png) b")
	if len(media) != 1 || media[0].URL != "http://img/1.png" || media[0].Kind != "image" {
		t.Fatalf("media = %+v", media)
	}
	if strings.Contains(rest, "![") {
		t.Errorf("rest still contains image syntax: %q", rest)
	}

	// No image: text returned unchanged, no media.
	media, rest = c.ExtractMedia("just [a link](http://u) here")
	if len(media) != 0 {
		t.Errorf("non-image link must not be extracted, got %+v", media)
	}
	if rest != "just [a link](http://u) here" {
		t.Errorf("rest = %q, want unchanged", rest)
	}
}

func TestTruncate_ShortTextSingleChunk(t *testing.T) {
	c := newTestChannel()
	chunks := c.Truncate("short text")
	if len(chunks) != 1 || chunks[0] != "short text" {
		t.Fatalf("chunks = %#v, want single unchanged chunk", chunks)
	}
}

// TestSplitPreservingFences forces a split inside a code fence and asserts each
// chunk has balanced fences (even number of ``` lines) so neither half renders
// as broken code.
func TestSplitPreservingFences(t *testing.T) {
	text := "intro line\n```go\nL1\nL2\nL3\nL4\nL5\n```\noutro"
	chunks := channel.SplitPreservingFences(text, 20)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	for i, ch := range chunks {
		fences := 0
		for _, line := range strings.Split(ch, "\n") {
			if isFence(line) {
				fences++
			}
		}
		if fences%2 != 0 {
			t.Errorf("chunk %d has unbalanced fences (%d):\n%s", i, fences, ch)
		}
	}
}
