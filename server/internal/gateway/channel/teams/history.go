// Package teams — live chat-history projection (Microsoft Graph).
//
// FetchHistory makes the Teams adapter a channel.HistoryFetcher: it pulls one
// page of recent chat messages via the Microsoft Graph API
//
//	GET /v1.0/teams/{team-id}/channels/{channel-id}/messages
//	GET /v1.0/teams/{team-id}/channels/{channel-id}/messages/{message-id}/replies
//
// and maps the response into the neutral HistoryMessage shape. Teams does NOT
// expose chat history through the Bot Framework Connector — the only way to
// list past channel messages is the Graph endpoint, which carries its own auth
// scope and its own rate-limit envelope. Both are handled here so the rest of
// the gateway stays platform-neutral.
//
// The two routing modes the fetcher branches on:
//
//   - ExternalThreadID == "" → top-level channel history. The fetcher needs
//     (team-id, channel-id) from the conversation's Graph routing tuple
//     (ConversationRef.TeamID + GraphChannelID, primed by the runner on every
//     inbound). The agent's ExternalChatID is the conversation id; we look the
//     Graph tuple up in the ConversationStore.
//   - ExternalThreadID != "" → replies of one channel message. ExternalThreadID
//     is the Graph chatMessage id (the agent passes the message id of the
//     parent verbatim — Teams calls this the "replyToId" on each child).
//
// Rate limit handling: Graph returns 429 with a Retry-After header. The
// fetcher translates that into *channel.RateLimitedError so imhistory.Gate
// can block-and-retry; the agent never sees a 429.
//
// The auth path is the same AAD client-credentials flow the outbound Connector
// sender uses (outbound.go), but with a DIFFERENT scope — the Connector
// resource rejects a Graph-scoped token and vice versa, so the fetcher mints
// its own bearer with scope graph.microsoft.com/.default and caches it
// independently of the Connector token. Minting reuses the resolved AAD
// (client_id, client_secret) pair.
package teams

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// graphBaseURL is the Microsoft Graph root the fetcher talks to. Overridable
// via a package var so tests point it at a local fixture.
var graphBaseURL = "https://graph.microsoft.com/v1.0"

// graphTokenScope is the resource scope a Graph access token is audienced to.
// The Graph endpoint rejects a token minted for any other scope (including the
// Bot Framework Connector's api.botframework.com/.default), and the same
// client_credentials exchange against the same AAD authority yields both
// tokens — they are audienced, not authenticated, differently.
const graphTokenScope = "https://graph.microsoft.com/.default"

// teamsHistoryCap is the neutral per-request ceiling reported back to the
// agent. Microsoft Graph caps a single chatMessage listing at 50 per page
// ($top=50); the agent's Limit argument is silently clamped.
const teamsHistoryCap = 50

// Compile-time assertion: the Teams adapter is a HistoryFetcher.
var _ channel.HistoryFetcher = (*Channel)(nil)

// graphChatMessage is the subset of a Microsoft Graph chatMessage the fetcher
// uses. Graph returns a much richer shape; the projection is enough for the
// agent to reconstruct recent context, not the full native payload.
type graphChatMessage struct {
	ID              string    `json:"id"`
	ReplyToID       string    `json:"replyToId"`
	CreatedDateTime time.Time `json:"createdDateTime"`
	From            *struct {
		User *struct {
			ID          string `json:"id"`
			DisplayName string `json:"displayName"`
		} `json:"user"`
		Application *struct {
			ID          string `json:"id"`
			DisplayName string `json:"displayName"`
		} `json:"application"`
	} `json:"from"`
	Body struct {
		Content     string `json:"content"`
		ContentType string `json:"contentType"`
	} `json:"body"`
}

// graphListResponse is the chatMessage collection envelope Graph returns.
// @odata.nextLink is the cursor for the adjacent older page ("" when there is
// no more).
type graphListResponse struct {
	Value    []graphChatMessage `json:"value"`
	NextLink string             `json:"@odata.nextLink"`
}

// teamsHistoryLister is the subset of Graph FetchHistory needs. The default
// factory (defaultHistoryListerFactory) builds an HTTP-backed lister; tests
// pass a fake via WithHistoryLister.
type teamsHistoryLister interface {
	channelMessages(ctx context.Context, teamID, channelID string, top int) (msgs []graphChatMessage, next string, err error)
	channelMessageReplies(ctx context.Context, teamID, channelID, messageID string, top int) (msgs []graphChatMessage, next string, err error)
}

// teamsHistoryListerClient is the HTTP-backed history lister. It mints its own
// Graph bearer via a graphTokenSource — the AAD mint is the only stateful bit
// and lives on the token source, not on the lister.
type teamsHistoryListerClient struct {
	tokens graphTokenSource
	http   *http.Client
}

// graphTokenSource mints a Graph access token (scope graph.microsoft.com/.default).
// Distinct from the Bot Framework bearer (scope api.botframework.com/.default)
// in outbound.go — the two resources reject each other's tokens. The production
// implementation is the same AAD client_credentials flow that the Connector
// sender uses (outbound.go), just with a different scope; tests pass a fake.
type graphTokenSource interface {
	graphBearer(ctx context.Context) (string, error)
}

// aadGraphTokenSource mints a Graph bearer via the same AAD client_credentials
// flow the Connector sender uses (outbound.go), but with the Graph scope. It
// keeps its own cache, independent of the Connector bearer cache, because the
// two tokens have different expiry clocks and a single cache would race the
// scopes.
type aadGraphTokenSource struct {
	clientID     string
	clientSecret string
	tenantID     string
	httpClient   *http.Client

	mu       sync.Mutex
	token    string
	tokenExp time.Time
}

func (s *aadGraphTokenSource) graphBearer(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != "" && time.Until(s.tokenExp) > time.Minute {
		return s.token, nil
	}
	if s.clientID == "" || s.clientSecret == "" {
		return "", errNoAppCredentials
	}
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {s.clientID},
		"client_secret": {s.clientSecret},
		"scope":         {graphTokenScope},
	}
	tenant := s.tenantID
	if tenant == "" {
		tenant = "botframework.com"
	}
	endpoint := strings.TrimRight(aadAuthorityHost, "/") + "/" + tenant + "/oauth2/v2.0/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("teams channel: mint Graph token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("teams channel: AAD Graph token endpoint returned status %d", resp.StatusCode)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return "", fmt.Errorf("teams channel: decode Graph AAD token: %w", err)
	}
	if strings.TrimSpace(tok.AccessToken) == "" {
		return "", fmt.Errorf("teams channel: AAD Graph token response carried no access_token")
	}
	s.token = tok.AccessToken
	s.tokenExp = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	return s.token, nil
}

func (s *teamsHistoryListerClient) channelMessages(ctx context.Context, teamID, channelID string, top int) ([]graphChatMessage, string, error) {
	endpoint := fmt.Sprintf("%s/teams/%s/channels/%s/messages?$top=%d", graphBaseURL, url.PathEscape(teamID), url.PathEscape(channelID), top)
	return s.doList(ctx, endpoint)
}

func (s *teamsHistoryListerClient) channelMessageReplies(ctx context.Context, teamID, channelID, messageID string, top int) ([]graphChatMessage, string, error) {
	endpoint := fmt.Sprintf("%s/teams/%s/channels/%s/messages/%s/replies?$top=%d", graphBaseURL, url.PathEscape(teamID), url.PathEscape(channelID), url.PathEscape(messageID), top)
	return s.doList(ctx, endpoint)
}

func (s *teamsHistoryListerClient) doList(ctx context.Context, endpoint string) ([]graphChatMessage, string, error) {
	token, err := s.tokens.graphBearer(ctx)
	if err != nil {
		return nil, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("teams channel: GET %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("teams channel: read response: %w", err)
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, "", &channel.RateLimitedError{
			Platform:   channel.PlatformTeams,
			RetryAfter: graphRetryAfter(resp, body),
			Err:        fmt.Errorf("teams channel: Graph throttled (429)"),
		}
	}
	if resp.StatusCode/100 != 2 {
		return nil, "", fmt.Errorf("teams channel: GET %s returned status %d: %s", endpoint, resp.StatusCode, string(body))
	}
	var out graphListResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, "", fmt.Errorf("teams channel: decode Graph response: %w", err)
	}
	return out.Value, out.NextLink, nil
}

// graphRetryAfter pulls the Retry-After header (seconds) off a 429 response.
// An absent/blank header yields a sensible default; gate's backoff handles a
// short RetryAfter the same as a long one.
func graphRetryAfter(resp *http.Response, body []byte) time.Duration {
	if resp != nil {
		if h := strings.TrimSpace(resp.Header.Get("Retry-After")); h != "" {
			if n, err := parseRetryAfterSeconds(h); err == nil {
				return time.Duration(n) * time.Second
			}
		}
	}
	_ = body // future: parse error body for retryAfter if header is absent
	return 2 * time.Second
}

func parseRetryAfterSeconds(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, errors.New("empty retry-after")
	}
	var n int
	for _, r := range raw {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("retry-after not numeric: %q", raw)
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}

// FetchHistory returns a bounded, oldest-first page of recent chat messages
// for a Teams channel or a single message's replies.
//
// The Graph routing tuple (team-id, channel-id) is read from the
// ConversationStore keyed by ExternalChatID (the conversation id). The store
// is primed by the runner on every inbound — a personal/groupChat conversation
// carries no team-id and the fetcher returns an error so the caller can fall
// back to "no history available" (the never-fail contract is enforced by the
// handler, which reports an empty page on a fetcher error).
//
// SourceAppID is the bound workspace-bot's Microsoft App Id; the bot's AAD
// credentials are resolved per call through c.creds, so a rotated vault
// secret takes effect without recreating the channel.
func (c *Channel) FetchHistory(ctx context.Context, req channel.FetchHistoryRequest) (channel.FetchHistoryResult, error) {
	convID := strings.TrimSpace(req.ExternalChatID)
	if convID == "" {
		return channel.FetchHistoryResult{}, errors.New("teams history: conversation id required")
	}
	if c.convRefs == nil {
		return channel.FetchHistoryResult{}, errors.New("teams history: conversation store not configured")
	}
	ref, ok := c.convRefs.Get(convID)
	if !ok {
		return channel.FetchHistoryResult{}, fmt.Errorf("teams history: no conversation ref for %q (inbound never primed the cache)", convID)
	}
	teamID := strings.TrimSpace(ref.TeamID)
	channelID := strings.TrimSpace(ref.GraphChannelID)
	if teamID == "" || channelID == "" {
		return channel.FetchHistoryResult{}, fmt.Errorf("teams history: conversation ref for %q carries no team/channel id (personal/groupChat not addressable via Graph history)", convID)
	}

	lister, err := c.historyListerFor(ctx, req.SourceAppID)
	if err != nil {
		return channel.FetchHistoryResult{}, err
	}

	limit := req.Limit
	if limit <= 0 || limit > teamsHistoryCap {
		limit = teamsHistoryCap
	}

	threadID := strings.TrimSpace(req.ExternalThreadID)
	var (
		rawMsgs    []graphChatMessage
		nextCursor string
	)
	if threadID == "" {
		rawMsgs, nextCursor, err = lister.channelMessages(ctx, teamID, channelID, limit)
	} else {
		rawMsgs, nextCursor, err = lister.channelMessageReplies(ctx, teamID, channelID, threadID, limit)
	}
	if err != nil {
		// lister already wraps 429s in *channel.RateLimitedError; surface as-is.
		return channel.FetchHistoryResult{}, err
	}

	msgs := make([]channel.HistoryMessage, 0, len(rawMsgs))
	for _, m := range rawMsgs {
		msgs = append(msgs, graphMessageToHistory(m))
	}
	// Graph lists newest-first; reverse to the neutral oldest-first order.
	reverseHistory(msgs)
	if req.Limit > 0 && len(msgs) > req.Limit {
		msgs = msgs[len(msgs)-req.Limit:]
	}
	return channel.FetchHistoryResult{Messages: msgs, NextCursor: nextCursor, Cap: teamsHistoryCap}, nil
}

// historyListerFor returns the configured history lister, or builds a real
// one from the resolved AAD credential. Mirrors senderFor in outbound.go:
// per-call credential resolve, no caching.
func (c *Channel) historyListerFor(ctx context.Context, sourceAppID string) (teamsHistoryLister, error) {
	if c.historyLister != nil {
		return c.historyLister, nil
	}
	botID := strings.TrimSpace(sourceAppID)
	if botID == "" {
		botID = c.appID
	}
	cred, err := c.creds.Resolve(ctx, botID)
	if err != nil {
		return nil, err
	}
	return &teamsHistoryListerClient{
		tokens: &aadGraphTokenSource{
			clientID:     strings.TrimSpace(cred.AppID),
			clientSecret: strings.TrimSpace(cred.AppSecret),
			tenantID:     strings.TrimSpace(c.tenantID),
			httpClient:   &http.Client{Timeout: 15 * time.Second},
		},
		http: &http.Client{Timeout: 15 * time.Second},
	}, nil
}

// graphMessageToHistory maps a single Graph chatMessage into the neutral
// HistoryMessage shape. FromBot is the cheapest heuristic (non-nil from.application).
func graphMessageToHistory(m graphChatMessage) channel.HistoryMessage {
	var senderID, senderName string
	if m.From != nil {
		switch {
		case m.From.User != nil:
			senderID = m.From.User.ID
			senderName = m.From.User.DisplayName
		case m.From.Application != nil:
			senderID = m.From.Application.ID
			senderName = m.From.Application.DisplayName
		}
	}
	text := m.Body.Content
	if m.Body.ContentType == "html" {
		text = stripTagsSimple(m.Body.Content)
	}
	return channel.HistoryMessage{
		ExternalMessageID: m.ID,
		SenderID:          senderID,
		SenderName:        senderName,
		Text:              text,
		ThreadID:          m.ReplyToID,
		CreatedAt:         m.CreatedDateTime.UTC(),
		FromBot:           m.From != nil && m.From.Application != nil,
	}
}

// stripTagsSimple is a tiny HTML→plain helper. Teams Adaptive Card text fields
// and chatMessage bodies are usually html; a perfect projection is not the
// fetcher's job — the agent just needs readable context. We strip the obvious
// cases (block tags become newlines, others vanish) and leave entities alone.
func stripTagsSimple(html string) string {
	var b strings.Builder
	inTag := false
	for _, r := range html {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
			b.WriteRune(' ')
		case !inTag:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

func reverseHistory(m []channel.HistoryMessage) {
	for i, j := 0, len(m)-1; i < j; i, j = i+1, j-1 {
		m[i], m[j] = m[j], m[i]
	}
}