package channel

import (
	"context"
	"fmt"
	"time"
)

// HistoryMessage is one neutral chat message returned by a HistoryFetcher. It
// is a read-only projection of a platform message — enough for an agent to
// reconstruct recent context, not the full native payload. Non-text content
// (cards, attachments) collapses to a short textual marker.
type HistoryMessage struct {
	ExternalMessageID string
	SenderID          string    // platform sender id; the bot's own id is included
	SenderName        string    // display name when the adapter cheaply has it, else ""
	Text              string    // plain-text body
	ThreadID          string    // owning thread id; empty for a top-level message
	CreatedAt         time.Time // message timestamp (UTC)
	FromBot           bool      // authored by our own bot (an echo)
}

// FetchHistoryRequest asks a HistoryFetcher for a bounded page of recent
// messages in a chat, optionally scoped to a single thread.
type FetchHistoryRequest struct {
	ExternalChatID   string // required
	ExternalThreadID string // optional: scope to one thread
	// SourceAppID is the bound bot's platform app id (Slack/Discord app id,
	// Microsoft App Id for Teams, Feishu app_id). The fetcher resolves the
	// live bot credential via its Channel's CredentialResolver keyed on this —
	// a per-call resolve means a rotated vault secret takes effect without
	// recreating the channel. Empty means the fetcher's channel does not need
	// per-bot credentials (Feishu, which builds the transport per-call).
	SourceAppID string
	Limit       int    // requested count; the adapter clamps to its platform cap
	Cursor      string // opaque page cursor from a prior FetchHistoryResult
}

// FetchHistoryResult is a bounded page of messages ordered oldest-first, plus
// an opaque cursor for the adjacent older page ("" when there is no more).
type FetchHistoryResult struct {
	Messages   []HistoryMessage
	NextCursor string
	// Cap is the platform per-request ceiling the adapter clamped Limit to
	// (Slack 15, Discord 100, ...). The caller reports the true ceiling back to
	// the agent so it can size follow-up requests.
	Cap int
}

// HistoryFetcher is an OPTIONAL Channel capability: an adapter that can pull a
// bounded page of recent chat history live from the platform implements it.
// The internal history endpoint type-asserts for it; a platform that does not
// implement it degrades to "no history available" rather than failing.
type HistoryFetcher interface {
	FetchHistory(ctx context.Context, req FetchHistoryRequest) (FetchHistoryResult, error)
}

// RateLimitedError is returned by a HistoryFetcher when the platform throttled
// the request. RetryAfter carries the platform's suggested wait so the
// rate-limit layer can block-and-retry rather than surface a failure — the
// history tool must never fail on a transient throttle.
type RateLimitedError struct {
	Platform   Platform
	RetryAfter time.Duration
	Err        error
}

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("%s history rate limited, retry after %s: %v", e.Platform, e.RetryAfter, e.Err)
}

func (e *RateLimitedError) Unwrap() error { return e.Err }
