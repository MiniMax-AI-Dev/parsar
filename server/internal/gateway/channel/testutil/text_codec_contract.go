package testutil

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

type FormatCase struct {
	Name string
	Text string
	Want string
}

type TextCodecContract struct {
	Codec       channel.TextCodec
	MaxRunes    int
	FormatCases []FormatCase
}

func RunTextCodecContract(t *testing.T, contract TextCodecContract) {
	t.Helper()

	t.Run("format", func(t *testing.T) {
		for _, testCase := range contract.FormatCases {
			t.Run(testCase.Name, func(t *testing.T) {
				if got := contract.Codec.Format(testCase.Text); got != testCase.Want {
					t.Errorf("Format(%q) = %q, want %q", testCase.Text, got, testCase.Want)
				}
			})
		}
	})

	t.Run("short text remains one chunk", func(t *testing.T) {
		chunks := contract.Codec.Truncate("short text")
		if len(chunks) != 1 || chunks[0] != "short text" {
			t.Fatalf("chunks = %#v, want single unchanged chunk", chunks)
		}
	})

	t.Run("long fenced text preserves chunk contract", func(t *testing.T) {
		line := strings.Repeat("x", min(80, contract.MaxRunes/4))
		text := "intro\n```go\n" + strings.Repeat(line+"\n", contract.MaxRunes/len(line)+2) + "```\noutro"
		chunks := contract.Codec.Truncate(text)
		if len(chunks) < 2 {
			t.Fatalf("expected multiple chunks, got %d", len(chunks))
		}
		for index, chunk := range chunks {
			if utf8.RuneCountInString(chunk) > contract.MaxRunes {
				t.Errorf("chunk %d has %d runes, limit %d", index, utf8.RuneCountInString(chunk), contract.MaxRunes)
			}
			if countFenceLines(chunk)%2 != 0 {
				t.Errorf("chunk %d has unbalanced fences:\n%s", index, chunk)
			}
		}
		if !strings.HasPrefix(chunks[1], "```go\n") {
			t.Errorf("second chunk did not reopen the typed fence: %q", chunks[1])
		}
	})

	t.Run("extract media", func(t *testing.T) {
		media, rest := contract.Codec.ExtractMedia("before ![alt](https://img.test/a.png) after [link](https://x.test)")
		if len(media) != 1 || media[0].Kind != "image" || media[0].URL != "https://img.test/a.png" {
			t.Fatalf("media = %+v, want one image", media)
		}
		if strings.Contains(rest, "![alt]") || !strings.Contains(rest, "[link](https://x.test)") {
			t.Errorf("remaining text = %q", rest)
		}

		media, rest = contract.Codec.ExtractMedia("no images here")
		if media != nil || rest != "no images here" {
			t.Errorf("no-image result = (%v, %q)", media, rest)
		}
	})
}

func countFenceLines(text string) int {
	count := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			count++
		}
	}
	return count
}
