package channel

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
