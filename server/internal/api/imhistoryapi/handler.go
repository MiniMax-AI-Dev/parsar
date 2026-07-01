package imhistoryapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/imhistory"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// Deps bundles the handler's collaborators. Gate is optional (a nil Gate runs
// the fetcher directly); everything else is required.
type Deps struct {
	Store    conversationStore
	Resolver HistoryResolver
	Signer   *Signer
	Gate     *imhistory.Gate
}

type handler struct {
	deps Deps
}

// RegisterRoutes mounts the internal history endpoint. Mount it OUTSIDE any
// session middleware — the caller is a sandbox subprocess authenticated by the
// per-conversation bearer token, not a logged-in user.
func RegisterRoutes(r chi.Router, deps Deps) {
	h := &handler{deps: deps}
	r.Get("/internal/im/history", h.fetchHistory)
}

// message is one message in the wire response. Timestamps are RFC3339 so the
// agent reads them without unit ambiguity.
type message struct {
	ID        string `json:"id"`
	SenderID  string `json:"sender_id"`
	Sender    string `json:"sender,omitempty"`
	Text      string `json:"text"`
	ThreadID  string `json:"thread_id,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	FromBot   bool   `json:"from_bot,omitempty"`
}

type response struct {
	Messages   []message `json:"messages"`
	NextCursor string    `json:"next_cursor,omitempty"`
	Cap        int       `json:"cap"`
	Platform   string    `json:"platform,omitempty"`
}

// fetchHistory authenticates the token against the requested conversation,
// resolves the platform routing tuple, and returns a bounded live page. It is
// built to satisfy the tool's never-fail contract: an unsupported platform or a
// resolver miss yields an empty 200 page, not an error — only a bad request or
// an authentication failure is a non-200.
func (h *handler) fetchHistory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	conversationID := strings.TrimSpace(q.Get("conversation_id"))
	if conversationID == "" {
		writeError(w, http.StatusBadRequest, "missing conversation_id")
		return
	}
	if !h.authorized(r, conversationID) {
		writeError(w, http.StatusUnauthorized, "invalid token")
		return
	}

	ref, err := h.deps.Store.GetConversationIMRef(r.Context(), conversationID)
	if err != nil {
		if errors.Is(err, store.ErrUnknownConversation) {
			writeError(w, http.StatusNotFound, "unknown conversation")
			return
		}
		writeError(w, http.StatusInternalServerError, "resolve conversation")
		return
	}

	fetcher, platform, found := h.deps.Resolver.HistoryFetcher(r.Context(), ref)
	if !found || fetcher == nil {
		// Platform has no live-history adapter: hand back an empty page so the
		// tool call still succeeds.
		writeJSON(w, http.StatusOK, response{Messages: []message{}, Platform: ref.Platform})
		return
	}

	req := channel.FetchHistoryRequest{
		ExternalChatID:   ref.ExternalID,
		ExternalThreadID: ref.ExternalThreadID,
		Limit:            parseLimit(q.Get("limit")),
		Cursor:           strings.TrimSpace(q.Get("cursor")),
	}

	res, err := h.fetch(r.Context(), platform, fetcher, req)
	if err != nil {
		writeError(w, http.StatusBadGateway, "fetch history")
		return
	}
	writeJSON(w, http.StatusOK, toResponse(res, platform))
}

// fetch routes through the Gate when configured (serialization + retry +
// cache), else calls the fetcher directly.
func (h *handler) fetch(ctx context.Context, platform channel.Platform, f channel.HistoryFetcher, req channel.FetchHistoryRequest) (channel.FetchHistoryResult, error) {
	if h.deps.Gate != nil {
		return h.deps.Gate.Fetch(ctx, platform, f, req)
	}
	return f.FetchHistory(ctx, req)
}

func (h *handler) authorized(r *http.Request, conversationID string) bool {
	if h.deps.Signer == nil {
		return false
	}
	return h.deps.Signer.Verify(conversationID, bearerToken(r))
}

func toResponse(res channel.FetchHistoryResult, platform channel.Platform) response {
	msgs := make([]message, 0, len(res.Messages))
	for _, m := range res.Messages {
		var ts string
		if !m.CreatedAt.IsZero() {
			ts = m.CreatedAt.UTC().Format(time.RFC3339)
		}
		msgs = append(msgs, message{
			ID:        m.ExternalMessageID,
			SenderID:  m.SenderID,
			Sender:    m.SenderName,
			Text:      m.Text,
			ThreadID:  m.ThreadID,
			CreatedAt: ts,
			FromBot:   m.FromBot,
		})
	}
	return response{Messages: msgs, NextCursor: res.NextCursor, Cap: res.Cap, Platform: string(platform)}
}

// bearerToken pulls the token from Authorization: Bearer <t>, falling back to a
// ?token= query param so the subprocess can use whichever is simpler.
func bearerToken(r *http.Request) string {
	if raw := r.Header.Get("Authorization"); strings.HasPrefix(raw, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(raw, "Bearer "))
	}
	return strings.TrimSpace(r.URL.Query().Get("token"))
}

// parseLimit reads the requested count; 0/invalid means "adapter default" and
// the adapter clamps to its platform cap regardless.
func parseLimit(raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
