package teams

import (
	"strings"
	"testing"
)

func TestFormat_HeadingsToBold(t *testing.T) {
	in := "# Title\n## Sub\nplain **bold** line"
	got := newTestChannel().Format(in)
	if !strings.Contains(got, "**Title**") {
		t.Errorf("H1 not bolded: %q", got)
	}
	if !strings.Contains(got, "**Sub**") {
		t.Errorf("H2 not bolded: %q", got)
	}
	if !strings.Contains(got, "plain **bold** line") {
		t.Errorf("non-heading line altered: %q", got)
	}
}

func TestFormat_LeavesFencedCode(t *testing.T) {
	in := "```\n# not a heading\n```"
	got := newTestChannel().Format(in)
	if !strings.Contains(got, "# not a heading") {
		t.Errorf("a # inside a fence must not be bolded: %q", got)
	}
}

func TestTruncate_ShortPassthrough(t *testing.T) {
	got := newTestChannel().Truncate("short")
	if len(got) != 1 || got[0] != "short" {
		t.Errorf("short text must pass through as one chunk, got %v", got)
	}
}

func TestTruncate_SplitsAndPreservesFences(t *testing.T) {
	// Build a fenced block longer than the budget so the splitter must cut it
	// and re-open the fence on the next chunk.
	var b strings.Builder
	b.WriteString("```go\n")
	for range teamsMaxMessageLen {
		b.WriteString("x")
	}
	b.WriteString("\nmore\n```")
	chunks := newTestChannel().Truncate(b.String())
	if len(chunks) < 2 {
		t.Fatalf("expected a split, got %d chunk(s)", len(chunks))
	}
	// Every chunk must have balanced fences (an even count of ``` lines).
	for i, ch := range chunks {
		fences := strings.Count(ch, "```")
		if fences%2 != 0 {
			t.Errorf("chunk %d has unbalanced fences (%d ``` tokens)", i, fences)
		}
	}
}

func TestExtractMedia(t *testing.T) {
	in := "before ![alt](https://img.test/a.png) after [link](https://x.test)"
	media, rest := newTestChannel().ExtractMedia(in)
	if len(media) != 1 {
		t.Fatalf("expected 1 image, got %d", len(media))
	}
	if media[0].URL != "https://img.test/a.png" || media[0].Kind != "image" {
		t.Errorf("media = %+v", media[0])
	}
	if strings.Contains(rest, "![alt]") {
		t.Errorf("image syntax not removed: %q", rest)
	}
	if !strings.Contains(rest, "[link](https://x.test)") {
		t.Errorf("non-image link must survive: %q", rest)
	}
}

func TestExtractMedia_NoImages(t *testing.T) {
	media, rest := newTestChannel().ExtractMedia("no images here")
	if media != nil {
		t.Errorf("expected nil media, got %v", media)
	}
	if rest != "no images here" {
		t.Errorf("text altered: %q", rest)
	}
}
