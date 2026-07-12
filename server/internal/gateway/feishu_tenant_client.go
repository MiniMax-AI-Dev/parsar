package gateway

import (
	"bytes"
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
)

// FeishuTenantClient talks to the Feishu open-platform APIs scoped to
// a single self-built Application. The client owns token lifecycle:
// callers pass the Bot's app_secret per-call, the client exchanges
// (app_id, app_secret) for a fresh token, caches the *token* until
// shortly before expiry, and refreshes on the next send.
//
// The app_secret is NEVER persisted by the client — only the
// tenant_access_token is cached. Callers fetch app_secret from the
// vault per-run so rotations take effect immediately.
type FeishuTenantClient struct {
	baseURL    string
	appID      string
	httpClient *http.Client

	now func() time.Time

	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

// FeishuTenantClientOptions configures FeishuTenantClient. AppID is
// required; app_secret is passed separately per-call so vault rotation
// takes effect immediately.
type FeishuTenantClientOptions struct {
	BaseURL    string // default: open.feishu.cn
	AppID      string
	HTTPClient *http.Client
	Now        func() time.Time // injectable for tests
}

// feishuTokenRefreshMargin: refresh this far ahead of true expiry so a
// concurrent send doesn't race a token expiring mid-flight.
const feishuTokenRefreshMargin = 2 * time.Minute

// feishuTokenMaxCacheLifetime caps our cache regardless of upstream
// expires_in (defence in depth; Feishu documents 7200s default).
const feishuTokenMaxCacheLifetime = 110 * time.Minute

// Default open-platform base URL (production).
const defaultFeishuOpenAPIBaseURL = "https://open.feishu.cn"

var (
	ErrFeishuTenantClientConfig    = errors.New("feishu tenant client misconfigured")
	ErrFeishuTokenExchangeFailed   = errors.New("feishu tenant_access_token exchange failed")
	ErrFeishuTokenExchangeRejected = errors.New("feishu tenant_access_token exchange rejected by upstream")
)

// NewFeishuTenantClient validates options and returns a ready client.
// BaseURL defaults to https://open.feishu.cn. app_secret is NOT held
// by the client — pass on each call.
func NewFeishuTenantClient(opts FeishuTenantClientOptions) (*FeishuTenantClient, error) {
	appID := strings.TrimSpace(opts.AppID)
	if appID == "" {
		return nil, fmt.Errorf("%w: app_id required", ErrFeishuTenantClientConfig)
	}
	baseURL := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultFeishuOpenAPIBaseURL
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &FeishuTenantClient{
		baseURL:    baseURL,
		appID:      appID,
		httpClient: httpClient,
		now:        now,
	}, nil
}

type feishuTenantTokenResponse struct {
	Code              int    `json:"code"`
	Msg               string `json:"msg"`
	TenantAccessToken string `json:"tenant_access_token"`
	Expire            int    `json:"expire"`
}

// fetchTenantAccessToken exchanges (app_id, app_secret) for a fresh
// tenant_access_token. The app_secret is supplied per-call.
func (c *FeishuTenantClient) fetchTenantAccessToken(ctx context.Context, appSecret string) (token string, expiresIn time.Duration, err error) {
	appSecret = strings.TrimSpace(appSecret)
	if appSecret == "" {
		return "", 0, fmt.Errorf("%w: empty app_secret on fetch", ErrFeishuTenantClientConfig)
	}
	payload, err := json.Marshal(map[string]string{
		"app_id":     c.appID,
		"app_secret": appSecret,
	})
	if err != nil {
		return "", 0, err
	}
	endpoint := c.baseURL + "/open-apis/auth/v3/tenant_access_token/internal"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.httpClient.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("%w: %v", ErrFeishuTokenExchangeFailed, err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return "", 0, fmt.Errorf("%w: read body: %v", ErrFeishuTokenExchangeFailed, err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", 0, fmt.Errorf("%w: http %d: %s", ErrFeishuTokenExchangeFailed, res.StatusCode, truncateForError(body))
	}
	var parsed feishuTenantTokenResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", 0, fmt.Errorf("%w: decode: %v", ErrFeishuTokenExchangeFailed, err)
	}
	if parsed.Code != 0 || strings.TrimSpace(parsed.TenantAccessToken) == "" {
		return "", 0, fmt.Errorf("%w: code=%d msg=%s", ErrFeishuTokenExchangeRejected, parsed.Code, parsed.Msg)
	}
	expires := time.Duration(parsed.Expire) * time.Second
	if expires <= 0 {
		expires = feishuTokenMaxCacheLifetime
	}
	if expires > feishuTokenMaxCacheLifetime {
		expires = feishuTokenMaxCacheLifetime
	}
	return parsed.TenantAccessToken, expires, nil
}

// TenantAccessToken returns a valid token, refreshing when cache is
// missing or near expiry. Thread-safe; one in-flight fetch even under
// concurrent calls. app_secret is consumed only on cache misses.
func (c *FeishuTenantClient) TenantAccessToken(ctx context.Context, appSecret string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.token != "" && c.now().Add(feishuTokenRefreshMargin).Before(c.expiresAt) {
		return c.token, nil
	}
	token, expiresIn, err := c.fetchTenantAccessToken(ctx, appSecret)
	if err != nil {
		return "", err
	}
	c.token = token
	c.expiresAt = c.now().Add(expiresIn)
	return c.token, nil
}

// InvalidateTokenCache clears the cached tenant_access_token. Operators
// call this from credential-rotation endpoints so the next send forces
// a fresh exchange against the new app_secret.
func (c *FeishuTenantClient) InvalidateTokenCache() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.token = ""
	c.expiresAt = time.Time{}
}

// FeishuMessageSendRequest is the upstream payload for
// POST /open-apis/im/v1/messages?receive_id_type=<type>.
type FeishuMessageSendRequest struct {
	ReceiveIDType string `json:"-"` // query param, not body
	ReceiveID     string `json:"receive_id"`
	MsgType       string `json:"msg_type"`
	Content       string `json:"content"`
	UUID          string `json:"uuid,omitempty"` // optional dedup key
	ReplyInThread bool   `json:"reply_in_thread,omitempty"`
}

// FeishuMessageReplyRequest is the upstream payload for
// POST /open-apis/im/v1/messages/{message_id}/reply.
type FeishuMessageReplyRequest struct {
	MsgType       string `json:"msg_type"`
	Content       string `json:"content"`
	UUID          string `json:"uuid,omitempty"`
	ReplyInThread bool   `json:"reply_in_thread,omitempty"`
}

// FeishuMessageSendResult is the small projection of the upstream
// response we care about: the Feishu message_id (for audit / dedup) and
// the raw code (so callers can distinguish "rejected by upstream" from
// network errors).
type FeishuMessageSendResult struct {
	MessageID string
	Code      int
	Msg       string
}

// FeishuBotInfo is the small projection Parsar needs after QR
// provisioning: validating fresh app credentials and capturing the
// bot open_id for self-message dedup.
type FeishuBotInfo struct {
	AppName string
	OpenID  string
}

// SendMessage POSTs im/v1/messages with the cached (or freshly
// fetched) tenant_access_token.
//
// Errors fall into three buckets:
//   - ErrFeishuTokenExchangeFailed / ErrFeishuTokenExchangeRejected
//     when the token round-trip fails.
//   - ErrFeishuNon2xx when the send returns non-2xx.
//   - ErrFeishuInvalidResponse for decode failure or non-zero code.
func (c *FeishuTenantClient) SendMessage(ctx context.Context, appSecret string, req FeishuMessageSendRequest) (FeishuMessageSendResult, error) {
	token, err := c.TenantAccessToken(ctx, appSecret)
	if err != nil {
		return FeishuMessageSendResult{}, err
	}

	receiveIDType := strings.TrimSpace(req.ReceiveIDType)
	if receiveIDType == "" {
		receiveIDType = "chat_id"
	}
	body, err := json.Marshal(req)
	if err != nil {
		return FeishuMessageSendResult{}, err
	}
	endpoint := c.baseURL + "/open-apis/im/v1/messages?receive_id_type=" + url.QueryEscape(receiveIDType)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return FeishuMessageSendResult{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json; charset=utf-8")

	res, err := c.httpClient.Do(httpReq)
	if err != nil {
		return FeishuMessageSendResult{}, err
	}
	defer res.Body.Close()
	respBytes, err := io.ReadAll(res.Body)
	if err != nil {
		return FeishuMessageSendResult{}, fmt.Errorf("%w: read body: %v", ErrFeishuInvalidResponse, err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return FeishuMessageSendResult{}, fmt.Errorf("%w: status=%d body=%s", ErrFeishuNon2xx, res.StatusCode, truncateForError(respBytes))
	}
	var parsed feishuSendAPIResponse
	if err := json.Unmarshal(respBytes, &parsed); err != nil {
		return FeishuMessageSendResult{}, fmt.Errorf("%w: %v", ErrFeishuInvalidResponse, err)
	}
	if parsed.Code != 0 {
		return FeishuMessageSendResult{
			Code: parsed.Code,
			Msg:  parsed.Msg,
		}, fmt.Errorf("%w: code=%d msg=%s", ErrFeishuInvalidResponse, parsed.Code, parsed.Msg)
	}
	mid := strings.TrimSpace(parsed.Data.MessageID)
	if mid == "" {
		return FeishuMessageSendResult{Code: parsed.Code}, fmt.Errorf("%w: missing message_id", ErrFeishuInvalidResponse)
	}
	return FeishuMessageSendResult{MessageID: mid, Code: 0, Msg: parsed.Msg}, nil
}

// ReplyMessage replies to an existing Feishu message. When ReplyInThread
// is true, Feishu creates/continues the topic thread anchored at
// messageID.
func (c *FeishuTenantClient) ReplyMessage(ctx context.Context, appSecret string, messageID string, req FeishuMessageReplyRequest) (FeishuMessageSendResult, error) {
	token, err := c.TenantAccessToken(ctx, appSecret)
	if err != nil {
		return FeishuMessageSendResult{}, err
	}
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return FeishuMessageSendResult{}, fmt.Errorf("%w: message_id required for reply", ErrFeishuTenantClientConfig)
	}
	body, err := json.Marshal(req)
	if err != nil {
		return FeishuMessageSendResult{}, err
	}
	endpoint := c.baseURL + "/open-apis/im/v1/messages/" + url.PathEscape(messageID) + "/reply"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return FeishuMessageSendResult{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json; charset=utf-8")

	res, err := c.httpClient.Do(httpReq)
	if err != nil {
		return FeishuMessageSendResult{}, err
	}
	defer res.Body.Close()
	respBytes, err := io.ReadAll(res.Body)
	if err != nil {
		return FeishuMessageSendResult{}, fmt.Errorf("%w: read body: %v", ErrFeishuInvalidResponse, err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return FeishuMessageSendResult{}, fmt.Errorf("%w: status=%d body=%s", ErrFeishuNon2xx, res.StatusCode, truncateForError(respBytes))
	}
	var parsed feishuSendAPIResponse
	if err := json.Unmarshal(respBytes, &parsed); err != nil {
		return FeishuMessageSendResult{}, fmt.Errorf("%w: %v", ErrFeishuInvalidResponse, err)
	}
	if parsed.Code != 0 {
		return FeishuMessageSendResult{Code: parsed.Code, Msg: parsed.Msg}, fmt.Errorf("%w: code=%d msg=%s", ErrFeishuInvalidResponse, parsed.Code, parsed.Msg)
	}
	mid := strings.TrimSpace(parsed.Data.MessageID)
	if mid == "" {
		return FeishuMessageSendResult{Code: parsed.Code}, fmt.Errorf("%w: missing message_id", ErrFeishuInvalidResponse)
	}
	return FeishuMessageSendResult{MessageID: mid, Code: 0, Msg: parsed.Msg}, nil
}

// PatchMessage updates the content of an already-delivered Feishu
// message. The inflight-card patch loop uses it to keep the
// "executing" card pinned to one message_id.
//
// `content` must be the JSON interactive-card payload (same shape as
// MarshalCard). A non-2xx HTTP or non-zero `code` is wrapped in
// ErrFeishuNon2xx / ErrFeishuInvalidResponse — callers distinguish
// transient transport errors from permanent rejections (e.g. past
// Feishu's 24h edit window — clear the inflight slot rather than retry).
func (c *FeishuTenantClient) PatchMessage(ctx context.Context, appSecret string, messageID string, content string) error {
	token, err := c.TenantAccessToken(ctx, appSecret)
	if err != nil {
		return err
	}
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return fmt.Errorf("%w: message_id required for patch", ErrFeishuTenantClientConfig)
	}
	body, err := json.Marshal(map[string]string{"content": content})
	if err != nil {
		return err
	}
	endpoint := c.baseURL + "/open-apis/im/v1/messages/" + url.PathEscape(messageID)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPatch, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json; charset=utf-8")

	res, err := c.httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	respBytes, err := io.ReadAll(res.Body)
	if err != nil {
		return fmt.Errorf("%w: read body: %v", ErrFeishuInvalidResponse, err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("%w: status=%d body=%s", ErrFeishuNon2xx, res.StatusCode, truncateForError(respBytes))
	}
	var parsed struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(respBytes, &parsed); err != nil {
		return fmt.Errorf("%w: %v", ErrFeishuInvalidResponse, err)
	}
	if parsed.Code != 0 {
		return fmt.Errorf("%w: code=%d msg=%s", ErrFeishuInvalidResponse, parsed.Code, parsed.Msg)
	}
	return nil
}

// FeishuFetchedMessage is the projection of GET im/v1/messages the
// quote-chain walker consumes. Only the fields the walker reads are
// populated; the upstream schema has more.
type FeishuFetchedMessage struct {
	MessageID      string
	MsgType        string
	BodyContent    string
	ParentID       string
	UpperMessageID string
	ChatID         string

	// History-projection fields: only ListMessagesByChatPage populates these
	// (the GET-by-id path leaves them zero). CreateTime is a millisecond epoch
	// string; SenderType "app" marks a bot-authored message.
	RootID     string
	ThreadID   string
	CreateTime string
	SenderID   string
	SenderType string

	// SubItems is non-empty only when the upstream response carries
	// data.items beyond items[0] — Feishu's GET on a merge_forward
	// message sometimes inlines the sub-messages here. When empty on
	// a merge_forward, callers fall back to ListMessagesByChat.
	SubItems []FeishuFetchedMessage
}

// feishuGetMessageMaxBytes caps response buffering for GET im/v1/messages.
// A normal message body is a few KB; 1 MiB is far above that yet small
// enough that a misbehaving upstream cannot OOM the gateway.
const feishuGetMessageMaxBytes = 1 << 20

// GetMessage fetches a single Feishu message by ID. The quote-chain
// walker tolerates errors — it just stops climbing.
//
// On a merge_forward parent, Feishu sometimes inlines sub-messages in
// data.items[1..]. We surface them via SubItems so the caller can
// render the forwarded conversation without a second round-trip when
// the data is already on the wire.
func (c *FeishuTenantClient) GetMessage(ctx context.Context, appSecret string, messageID string) (FeishuFetchedMessage, error) {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return FeishuFetchedMessage{}, fmt.Errorf("%w: message_id required for get", ErrFeishuTenantClientConfig)
	}
	type rawItem struct {
		MessageID      string `json:"message_id"`
		MsgType        string `json:"msg_type"`
		ParentID       string `json:"parent_id"`
		UpperMessageID string `json:"upper_message_id"`
		ChatID         string `json:"chat_id"`
		Body           struct {
			Content string `json:"content"`
		} `json:"body"`
	}
	var parsed struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Items []rawItem `json:"items"`
		} `json:"data"`
	}
	if err := c.doAuthedJSONGet(ctx, appSecret,
		"/open-apis/im/v1/messages/"+url.PathEscape(messageID),
		feishuGetMessageMaxBytes, &parsed); err != nil {
		return FeishuFetchedMessage{}, err
	}
	if parsed.Code != 0 {
		return FeishuFetchedMessage{}, fmt.Errorf("%w: code=%d msg=%s", ErrFeishuInvalidResponse, parsed.Code, parsed.Msg)
	}
	if len(parsed.Data.Items) == 0 {
		return FeishuFetchedMessage{}, fmt.Errorf("%w: no items", ErrFeishuInvalidResponse)
	}
	convert := func(r rawItem) FeishuFetchedMessage {
		return FeishuFetchedMessage{
			MessageID:      strings.TrimSpace(r.MessageID),
			MsgType:        strings.TrimSpace(r.MsgType),
			BodyContent:    r.Body.Content,
			ParentID:       strings.TrimSpace(r.ParentID),
			UpperMessageID: strings.TrimSpace(r.UpperMessageID),
			ChatID:         strings.TrimSpace(r.ChatID),
		}
	}
	root := convert(parsed.Data.Items[0])
	if root.MessageID == "" {
		root.MessageID = messageID
	}
	for _, sub := range parsed.Data.Items[1:] {
		converted := convert(sub)
		// Defensive: skip the self entry if Feishu repeats it; we only
		// want the children. Some payload shapes include the parent in
		// items[0] only, others duplicate it — guard either way.
		if converted.MessageID == "" || converted.MessageID == root.MessageID {
			continue
		}
		root.SubItems = append(root.SubItems, converted)
	}
	return root, nil
}

// feishuListMessagesPageSize is Feishu's max page_size for
// im/v1/messages list. We pick the max to minimise round-trips when
// walking back from the trigger to locate merge_forward children.
const feishuListMessagesPageSize = 50

// ListMessagesByChatPage fetches one page of messages in a chat,
// newest first. The merge_forward fallback uses this to find child
// messages by upper_message_id when GetMessage didn't inline them.
//
// pageToken is empty on the first call; pass back the returned
// nextPageToken to continue. nextPageToken is "" when Feishu has no
// more pages.
func (c *FeishuTenantClient) ListMessagesByChatPage(ctx context.Context, appSecret, chatID, pageToken string) ([]FeishuFetchedMessage, string, error) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return nil, "", fmt.Errorf("%w: chat_id required for list", ErrFeishuTenantClientConfig)
	}
	q := url.Values{}
	q.Set("container_id_type", "chat")
	q.Set("container_id", chatID)
	q.Set("page_size", fmt.Sprintf("%d", feishuListMessagesPageSize))
	q.Set("sort_type", "ByCreateTimeDesc")
	if pageToken = strings.TrimSpace(pageToken); pageToken != "" {
		q.Set("page_token", pageToken)
	}
	type rawItem struct {
		MessageID      string `json:"message_id"`
		MsgType        string `json:"msg_type"`
		ParentID       string `json:"parent_id"`
		UpperMessageID string `json:"upper_message_id"`
		ChatID         string `json:"chat_id"`
		RootID         string `json:"root_id"`
		ThreadID       string `json:"thread_id"`
		CreateTime     string `json:"create_time"`
		Sender         struct {
			ID         string `json:"id"`
			SenderType string `json:"sender_type"`
		} `json:"sender"`
		Body struct {
			Content string `json:"content"`
		} `json:"body"`
	}
	var parsed struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			HasMore   bool      `json:"has_more"`
			PageToken string    `json:"page_token"`
			Items     []rawItem `json:"items"`
		} `json:"data"`
	}
	if err := c.doAuthedJSONGet(ctx, appSecret,
		"/open-apis/im/v1/messages?"+q.Encode(),
		feishuGetMessageMaxBytes, &parsed); err != nil {
		return nil, "", err
	}
	if parsed.Code != 0 {
		return nil, "", fmt.Errorf("%w: code=%d msg=%s", ErrFeishuInvalidResponse, parsed.Code, parsed.Msg)
	}
	items := make([]FeishuFetchedMessage, 0, len(parsed.Data.Items))
	for _, r := range parsed.Data.Items {
		items = append(items, FeishuFetchedMessage{
			MessageID:      strings.TrimSpace(r.MessageID),
			MsgType:        strings.TrimSpace(r.MsgType),
			BodyContent:    r.Body.Content,
			ParentID:       strings.TrimSpace(r.ParentID),
			UpperMessageID: strings.TrimSpace(r.UpperMessageID),
			ChatID:         strings.TrimSpace(r.ChatID),
			RootID:         strings.TrimSpace(r.RootID),
			ThreadID:       strings.TrimSpace(r.ThreadID),
			CreateTime:     strings.TrimSpace(r.CreateTime),
			SenderID:       strings.TrimSpace(r.Sender.ID),
			SenderType:     strings.TrimSpace(r.Sender.SenderType),
		})
	}
	next := ""
	if parsed.Data.HasMore {
		next = strings.TrimSpace(parsed.Data.PageToken)
	}
	return items, next, nil
}

// doAuthedJSONGet performs an authenticated GET, caps the response body
// at maxBytes (so a misbehaving upstream can't OOM the gateway), and
// unmarshals into out. Returns ErrFeishuNon2xx for HTTP failures and
// ErrFeishuInvalidResponse for decode errors. Send/Reply/Patch still
// carry their own scaffolds — they predate this helper and tightening
// them is left to a follow-up so this MR stays focused.
func (c *FeishuTenantClient) doAuthedJSONGet(ctx context.Context, appSecret, path string, maxBytes int64, out any) error {
	token, err := c.TenantAccessToken(ctx, appSecret)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	limited := io.LimitReader(res.Body, maxBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("%w: read body: %v", ErrFeishuInvalidResponse, err)
	}
	if int64(len(body)) > maxBytes {
		return fmt.Errorf("%w: response exceeded %d byte cap", ErrFeishuInvalidResponse, maxBytes)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("%w: status=%d body=%s", ErrFeishuNon2xx, res.StatusCode, truncateForError(body))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%w: %v", ErrFeishuInvalidResponse, err)
	}
	return nil
}

// DefaultTypingReactionEmoji renders as the keyboard hands emoji in
// Feishu — the "I'm working on it" signal before the Done card lands.
// Feishu's emoji_type is a fixed enum (not Unicode codepoints).
const DefaultTypingReactionEmoji = "Typing"

// AddReaction attaches an emoji reaction to a message and returns the
// reaction_id Feishu hands back (needed for DeleteReaction — there's
// no "delete by emoji type" primitive).
//
// Used on the inbound side for immediate visual confirmation that the
// message was accepted. Caller treats failure as non-fatal — losing
// the indicator is annoying but not a correctness issue.
func (c *FeishuTenantClient) AddReaction(ctx context.Context, appSecret string, messageID string, emojiType string) (string, error) {
	token, err := c.TenantAccessToken(ctx, appSecret)
	if err != nil {
		return "", err
	}
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return "", fmt.Errorf("%w: message_id required for reaction", ErrFeishuTenantClientConfig)
	}
	emojiType = strings.TrimSpace(emojiType)
	if emojiType == "" {
		emojiType = DefaultTypingReactionEmoji
	}
	body, err := json.Marshal(map[string]any{
		"reaction_type": map[string]string{"emoji_type": emojiType},
	})
	if err != nil {
		return "", err
	}
	endpoint := c.baseURL + "/open-apis/im/v1/messages/" + url.PathEscape(messageID) + "/reactions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json; charset=utf-8")

	res, err := c.httpClient.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	respBytes, err := io.ReadAll(res.Body)
	if err != nil {
		return "", fmt.Errorf("%w: read body: %v", ErrFeishuInvalidResponse, err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", fmt.Errorf("%w: status=%d body=%s", ErrFeishuNon2xx, res.StatusCode, truncateForError(respBytes))
	}
	var parsed struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			ReactionID string `json:"reaction_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBytes, &parsed); err != nil {
		return "", fmt.Errorf("%w: %v", ErrFeishuInvalidResponse, err)
	}
	if parsed.Code != 0 {
		return "", fmt.Errorf("%w: code=%d msg=%s", ErrFeishuInvalidResponse, parsed.Code, parsed.Msg)
	}
	return strings.TrimSpace(parsed.Data.ReactionID), nil
}

// DeleteReaction removes a previously-added reaction. Called from the
// outbound terminal path so the typing indicator clears at the moment
// the reply lands. Reaction-already-gone returns a non-zero code from
// Feishu (surfaced as ErrFeishuInvalidResponse); caller logs and moves
// on rather than retrying.
func (c *FeishuTenantClient) DeleteReaction(ctx context.Context, appSecret string, messageID string, reactionID string) error {
	token, err := c.TenantAccessToken(ctx, appSecret)
	if err != nil {
		return err
	}
	messageID = strings.TrimSpace(messageID)
	reactionID = strings.TrimSpace(reactionID)
	if messageID == "" || reactionID == "" {
		return fmt.Errorf("%w: message_id and reaction_id required for reaction delete", ErrFeishuTenantClientConfig)
	}
	endpoint := c.baseURL + "/open-apis/im/v1/messages/" + url.PathEscape(messageID) + "/reactions/" + url.PathEscape(reactionID)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)

	res, err := c.httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	respBytes, err := io.ReadAll(res.Body)
	if err != nil {
		return fmt.Errorf("%w: read body: %v", ErrFeishuInvalidResponse, err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("%w: status=%d body=%s", ErrFeishuNon2xx, res.StatusCode, truncateForError(respBytes))
	}
	var parsed struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(respBytes, &parsed); err != nil {
		return fmt.Errorf("%w: %v", ErrFeishuInvalidResponse, err)
	}
	if parsed.Code != 0 {
		return fmt.Errorf("%w: code=%d msg=%s", ErrFeishuInvalidResponse, parsed.Code, parsed.Msg)
	}
	return nil
}

// BotInfo validates app credentials by fetching the Bot profile.
// Response shape has drifted across SDKs; we accept the common `bot`
// object and extract only stable optional strings. A code=0 with no
// app_name is still valid; older tenants omit it.
func (c *FeishuTenantClient) BotInfo(ctx context.Context, appSecret string) (FeishuBotInfo, error) {
	token, err := c.TenantAccessToken(ctx, appSecret)
	if err != nil {
		return FeishuBotInfo{}, err
	}
	endpoint := c.baseURL + "/open-apis/bot/v3/info/"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return FeishuBotInfo{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := c.httpClient.Do(req)
	if err != nil {
		return FeishuBotInfo{}, err
	}
	defer res.Body.Close()
	respBytes, err := io.ReadAll(res.Body)
	if err != nil {
		return FeishuBotInfo{}, fmt.Errorf("%w: read body: %v", ErrFeishuInvalidResponse, err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return FeishuBotInfo{}, fmt.Errorf("%w: status=%d body=%s", ErrFeishuNon2xx, res.StatusCode, truncateForError(respBytes))
	}
	var parsed struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Bot  struct {
			AppName string `json:"app_name"`
			OpenID  string `json:"open_id"`
		} `json:"bot"`
	}
	if err := json.Unmarshal(respBytes, &parsed); err != nil {
		return FeishuBotInfo{}, fmt.Errorf("%w: %v", ErrFeishuInvalidResponse, err)
	}
	if parsed.Code != 0 {
		return FeishuBotInfo{}, fmt.Errorf("%w: code=%d msg=%s", ErrFeishuInvalidResponse, parsed.Code, parsed.Msg)
	}
	return FeishuBotInfo{
		AppName: strings.TrimSpace(parsed.Bot.AppName),
		OpenID:  strings.TrimSpace(parsed.Bot.OpenID),
	}, nil
}

// BuildFeishuTextContent wraps a plain string as the JSON content
// field Feishu expects when msg_type=text:
//
//	{"text": "hello"}
//
// Caller passes the result as FeishuMessageSendRequest.Content.
func BuildFeishuTextContent(text string) (string, error) {
	raw, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// These constants are the visual defaults for the simple notice card
// we ship out of the outbound worker. Future builders (streaming /
// done / permission) can compose them — these are the minimum-viable
// values for the plain-reply form.
const (
	FeishuCardSchema          = "2.0"
	FeishuCardDefaultTitle    = "Parsar Agent"
	FeishuCardDefaultTemplate = "blue"
)

// BuildFeishuInteractiveContent wraps a plain reply string as a
// schema 2.0 interactive card with a single markdown element.
//
// Deprecated: prefer BuildFeishuDoneCardContent / NoticeCardContent /
// ErrorCardContent for kind-specific visual treatment. Retained as
// the immediate-reply fallback when the outbound worker can't infer
// a CardKind from message metadata.
func BuildFeishuInteractiveContent(text string) (string, error) {
	body := strings.TrimSpace(text)
	if body == "" {
		body = " "
	}
	card := map[string]any{
		"schema": FeishuCardSchema,
		"config": map[string]any{
			"wide_screen_mode": true,
		},
		"header": map[string]any{
			"title": map[string]any{
				"tag":     "plain_text",
				"content": FeishuCardDefaultTitle,
			},
			"template": FeishuCardDefaultTemplate,
		},
		"body": map[string]any{
			"elements": []map[string]any{
				{
					"tag":     "markdown",
					"content": body,
				},
			},
		},
	}
	raw, err := json.Marshal(card)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// BuildFeishuDoneCardContent returns a JSON content string ready for
// FeishuMessageSendRequest.Content. steps / thinkingText / usage may
// be nil/empty/zero.
func BuildFeishuDoneCardContent(title, text string, steps []StepInfo, thinkingText string, elapsed time.Duration, usage *UsageStats) (string, error) {
	return MarshalCard(BuildDoneCard(title, text, steps, thinkingText, elapsed, usage))
}

// BuildFeishuNoticeCardContent wraps BuildNoticeCard for command-echo
// and guest-hint replies.
func BuildFeishuNoticeCardContent(title, body string, color NoticeColor) (string, error) {
	return MarshalCard(BuildNoticeCard(title, body, color))
}

// BuildFeishuErrorCardContent wraps BuildErrorCard. detailURL=""
// suppresses the "View this round" link when no public URL is configured.
// rawError is the un-mapped error from the run.failed event; guestHint
// is the unregistered-sender register prompt — see BuildErrorCard for
// when they surface.
func BuildFeishuErrorCardContent(title, message, rawError, detailURL, guestHint string) (string, error) {
	return MarshalCard(BuildErrorCard(title, message, rawError, detailURL, guestHint))
}

// BuildFeishuPermissionCardContent wraps BuildPermissionCard. The
// permission_request_id is embedded into the button payload so
// handleCardAction can route the verdict back to
// connector.SubmitPermission without an extra DB lookup.
func BuildFeishuPermissionCardContent(title, toolName, toolInput, permissionRequestID string) (string, error) {
	return MarshalCard(BuildPermissionCard(title, toolName, toolInput, permissionRequestID))
}

// BuildFeishuPermissionResultCardContent wraps BuildPermissionResultCard.
func BuildFeishuPermissionResultCardContent(title string, allowed bool) (string, error) {
	return MarshalCard(BuildPermissionResultCard(title, allowed))
}

// BuildFeishuPromptForUserChoiceCardContent wraps
// BuildPromptForUserChoiceCard. The request_id is embedded in the
// form submit value so handleCardAction can resolve the slot without
// a side-channel lookup.
func BuildFeishuPromptForUserChoiceCardContent(title string, questions []PromptForUserChoiceCardQuestion, requestID string) (string, error) {
	return MarshalCard(BuildPromptForUserChoiceCard(title, questions, requestID))
}

// FeishuMessageResource is the projection callers need after
// downloading a binary payload attached to an inbound. MIME is the
// upstream Content-Type; bytes are raw.
type FeishuMessageResource struct {
	MIME string
	Data []byte
}

// FeishuResourceType enumerates the `type=` query value the
// im/v1/messages/{message_id}/resources/{file_key} endpoint accepts.
// Typed enum prevents misspelled types silently 404'ing upstream.
type FeishuResourceType string

const (
	FeishuResourceTypeImage FeishuResourceType = "image"
	FeishuResourceTypeFile  FeishuResourceType = "file"
)

// feishuMaxMessageResourceBytes caps in-memory size before the caller
// stops reading. 10 MiB covers screenshots and small attachments
// without exposing the pod to OOM from a malicious large upload.
const feishuMaxMessageResourceBytes = 10 << 20

// DownloadMessageResource fetches a binary payload from an inbound
// message. Feishu validates that (file_key) really belongs to
// message_id before serving, so user-controlled file_keys are safe.
//
// Returns MIME (from upstream Content-Type) + bytes. Oversized
// payloads return ErrFeishuInvalidResponse with the cap referenced.
func (c *FeishuTenantClient) DownloadMessageResource(ctx context.Context, appSecret string, messageID string, fileKey string, resourceType FeishuResourceType) (FeishuMessageResource, error) {
	token, err := c.TenantAccessToken(ctx, appSecret)
	if err != nil {
		return FeishuMessageResource{}, err
	}
	messageID = strings.TrimSpace(messageID)
	fileKey = strings.TrimSpace(fileKey)
	if messageID == "" || fileKey == "" {
		return FeishuMessageResource{}, fmt.Errorf("%w: message_id and file_key required for resource download", ErrFeishuTenantClientConfig)
	}
	kind := strings.TrimSpace(string(resourceType))
	if kind == "" {
		kind = string(FeishuResourceTypeImage)
	}
	endpoint := c.baseURL + "/open-apis/im/v1/messages/" + url.PathEscape(messageID) + "/resources/" + url.PathEscape(fileKey) + "?type=" + url.QueryEscape(kind)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return FeishuMessageResource{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)

	res, err := c.httpClient.Do(httpReq)
	if err != nil {
		return FeishuMessageResource{}, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		// Read a bounded prefix purely for the error message.
		excerpt, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		return FeishuMessageResource{}, fmt.Errorf("%w: status=%d body=%s", ErrFeishuNon2xx, res.StatusCode, truncateForError(excerpt))
	}
	limited := io.LimitReader(res.Body, feishuMaxMessageResourceBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return FeishuMessageResource{}, fmt.Errorf("%w: read body: %v", ErrFeishuInvalidResponse, err)
	}
	if len(body) > feishuMaxMessageResourceBytes {
		return FeishuMessageResource{}, fmt.Errorf("%w: resource exceeded %d byte cap", ErrFeishuInvalidResponse, feishuMaxMessageResourceBytes)
	}
	mime := strings.TrimSpace(res.Header.Get("Content-Type"))
	if mime == "" {
		// Real Feishu responses always include Content-Type; this
		// path only fires under fakes / proxy oddities.
		switch resourceType {
		case FeishuResourceTypeImage:
			mime = "image/png"
		default:
			mime = "application/octet-stream"
		}
	}
	return FeishuMessageResource{MIME: mime, Data: body}, nil
}

// truncateForError keeps upstream error excerpts short so we don't
// blow up logs / audit rows when Feishu returns a giant HTML page.
func truncateForError(body []byte) string {
	const max = 256
	s := strings.TrimSpace(string(body))
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
