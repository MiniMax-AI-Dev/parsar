package channel

import "strings"

// TextCodec is an optional sub-interface for platform-aware text formatting
// and length-bounded splitting. Each platform has a different markup dialect
// and character cap, so the driver delegates formatting to the adapter.
type TextCodec interface {
	// Format converts neutral markup to the platform dialect,
	// e.g. "**bold**" -> Slack "*bold*".
	Format(text string) string
	// Truncate splits text into platform-sized chunks, preserving code-block
	// boundaries.
	Truncate(text string) []string
	// ExtractMedia pulls image/file references out, returning them plus the
	// remaining plain text.
	ExtractMedia(text string) ([]Media, string)
}

// Media is a reference extracted from message text by a TextCodec.
type Media struct {
	Kind string // "image" | "file" | "video"
	URL  string
	Key  string
}

// SplitPreservingFences splits text into ≤limit-rune chunks on line boundaries,
// keeping fenced code blocks valid across a split (a chunk that ends mid-fence
// is closed, and the next chunk reopened, with a fence token).
func SplitPreservingFences(text string, limit int) []string {
	runeLen := func(s string) int { return len([]rune(s)) }
	isFence := func(line string) bool {
		return strings.HasPrefix(strings.TrimSpace(line), "```")
	}

	if runeLen(text) <= limit {
		return []string{text}
	}
	srcLines := strings.Split(text, "\n")

	var (
		out      []string
		chunk    []string
		chunkLen int
		inFence  bool
		fenceTok = "```"
	)

	flush := func() {
		if len(chunk) == 0 {
			return
		}
		body := chunk
		if inFence {
			body = append(append([]string(nil), chunk...), fenceTok)
		}
		out = append(out, strings.Join(body, "\n"))
		chunk = nil
		chunkLen = 0
		if inFence {
			chunk = append(chunk, fenceTok)
			chunkLen = runeLen(fenceTok) + 1
		}
	}

	for _, line := range srcLines {
		add := runeLen(line) + 1 // +1 for the joining newline
		if chunkLen+add > limit && len(chunk) > 0 {
			flush()
		}
		chunk = append(chunk, line)
		chunkLen += add
		if isFence(line) {
			if !inFence {
				inFence = true
				if tok := strings.TrimSpace(line); tok != "" {
					fenceTok = tok
				}
			} else {
				inFence = false
				fenceTok = "```"
			}
		}
	}
	if len(chunk) > 0 {
		out = append(out, strings.Join(chunk, "\n"))
	}
	return out
}
