// Package imhistoryapi is the internal HTTP surface the auto-mounted
// fetch_chat_history MCP tool calls back into. The tool runs as a stdio
// subprocess inside the agent sandbox and cannot reach the live IM SDK clients
// directly; instead it makes an authenticated request here, and the server
// resolves the conversation, picks the right channel adapter, and pulls a live
// bounded page of chat history.
//
// Auth is a stateless per-conversation bearer token: HMAC-SHA256 over the
// conversation id keyed by a server secret. The token is minted once when the
// capability is injected into a run and handed to the subprocess via env, so
// the sandbox never holds the signing secret and a token grants access to
// exactly one conversation's history.
package imhistoryapi

import (
	"context"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// HistoryResolver returns a live channel.HistoryFetcher for a conversation's
// platform + bound bot. It abstracts the split between Feishu (built per-call
// from the worker's transport, keyed by source app id) and the registry-held
// Slack/Discord adapters, so the handler stays platform-agnostic. found is
// false when the platform has no history-capable adapter — the handler then
// degrades to an empty page rather than an error, preserving the tool's
// never-fail contract.
type HistoryResolver interface {
	HistoryFetcher(ctx context.Context, ref store.ConversationIMRef) (fetcher channel.HistoryFetcher, platform channel.Platform, found bool)
}

// conversationStore is the store subset the handler needs: resolve a
// conversation id to its IM routing tuple.
type conversationStore interface {
	GetConversationIMRef(ctx context.Context, conversationID string) (store.ConversationIMRef, error)
}
