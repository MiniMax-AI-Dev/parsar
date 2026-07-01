// Package teams — outbound transport (Bot Framework Connector REST).
//
// Reply / Send / Edit turn neutral channel calls into Bot Framework Connector
// calls: a reply/new message is a POST to
//
//	{serviceUrl}/v3/conversations/{conversationId}/activities
//
// and an edit is a PUT to that path plus /{activityId}. Two Teams-specific
// facts drive the shape:
//
//   - serviceUrl is per-conversation and has NO ReplyTarget slot (the neutral
//     contract never modelled a regional base URL). It rides the
//     ConversationStore the runner primes on each inbound; senderFor reads it
//     back keyed by target.ExternalChatID (the conversation id).
//   - Outbound auth is an AAD client-credentials bearer, minted from (app id,
//     password) and cached to its own expiry — NOT the inbound JWT verify.go
//     checks. The mint lives here, in connectorSender, so the two tokens never
//     share a type (the classic Bot Framework 401 is conflating them).
//
// To stay unit-testable without the HTTP client the adapter talks to a small
// teamsSender interface in explicit arguments; connectorSender is the
// production implementation and tests inject a fake via WithSenderFactory.
package teams

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// adaptiveCardContentType is the attachment contentType a Teams client renders
// as an Adaptive Card. The renderers (adaptivecard.go) produce the card
// "content" object this wraps.
const adaptiveCardContentType = "application/vnd.microsoft.card.adaptive"

// aadAuthorityHost is the AAD login host the client-credentials token is minted
// from. The tenant segment is the configured tenant id (single-tenant bot) or
// "botframework.com" (multi-tenant). Overridable via a package var so tests
// point it at a local fixture.
var aadAuthorityHost = "https://login.microsoftonline.com"

// aadTokenScope is the resource scope an outbound Connector token is audienced
// to. The Connector rejects a token minted for any other scope.
const aadTokenScope = "https://api.botframework.com/.default"

// teamsWireMessage is the payload shape the renderers emit: a fallback Text and
// an optional Adaptive Card "content" object. Reply sends Text only; the card
// renderers fill Card. It mirrors slackWireMessage so outbound decoding is one
// shape across the interactive paths.
type teamsWireMessage struct {
	Text string          `json:"text,omitempty"`
	Card json.RawMessage `json:"card,omitempty"`
}

// outboundActivity is the Bot Framework Activity body POSTed/PUT to the
// Connector. Type is always "message"; Attachments carries the Adaptive Card
// when present; ReplyToID threads a reply under the inbound message.
type outboundActivity struct {
	Type        string       `json:"type"`
	Text        string       `json:"text,omitempty"`
	Attachments []attachment `json:"attachments,omitempty"`
	ReplyToID   string       `json:"replyToId,omitempty"`
}

// attachment is one Activity attachment; for a card the contentType is
// adaptiveCardContentType and Content is the card "content" object.
type attachment struct {
	ContentType string          `json:"contentType"`
	Content     json.RawMessage `json:"content"`
}

// teamsSender is the minimal outbound surface the adapter needs, in explicit
// arguments so it is testable without a live Connector. connectorSender is the
// production implementation; tests pass a fake via WithSenderFactory.
type teamsSender interface {
	// send POSTs a new activity to the conversation and returns the created
	// activity id (the MessageRef id an Edit later targets).
	send(ctx context.Context, serviceURL, conversationID string, act outboundActivity) (string, error)
	// edit PUTs an existing activity in place.
	edit(ctx context.Context, serviceURL, conversationID, activityID string, act outboundActivity) error
}

// connectorSender is the production teamsSender. It mints and caches an AAD
// client-credentials bearer (per credential, to its own expiry) and issues the
// Connector REST calls. One sender is built per resolved credential so a
// multi-bot deployment keeps separate token caches.
type connectorSender struct {
	clientID     string
	clientSecret string
	tenantID     string
	httpClient   *http.Client

	mu       sync.Mutex
	token    string
	tokenExp time.Time
}

// defaultSenderFactory builds a real Connector-backed sender from a resolved
// credential. It closes over the adapter's tenantID (the AAD authority) and is
// wired as c.newSender in New so tests can override it via WithSenderFactory.
func (c *Channel) defaultSenderFactory(cred channel.Credential) teamsSender {
	return &connectorSender{
		clientID:     strings.TrimSpace(cred.AppID),
		clientSecret: strings.TrimSpace(cred.AppSecret),
		tenantID:     strings.TrimSpace(c.tenantID),
		httpClient:   &http.Client{Timeout: 15 * time.Second},
	}
}

// tokenAuthority returns the AAD token endpoint for this sender's tenant. A
// single-tenant bot pins its tenant id; a multi-tenant bot uses the shared
// botframework.com authority.
func (s *connectorSender) tokenAuthority() string {
	tenant := s.tenantID
	if tenant == "" {
		tenant = "botframework.com"
	}
	return strings.TrimRight(aadAuthorityHost, "/") + "/" + tenant + "/oauth2/v2.0/token"
}

// bearer returns a valid AAD access token, minting a fresh one when the cache
// is empty or within 60s of expiry. Serialized so concurrent sends mint once.
func (s *connectorSender) bearer(ctx context.Context) (string, error) {
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
		"scope":         {aadTokenScope},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.tokenAuthority(), strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("teams channel: mint AAD token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("teams channel: AAD token endpoint returned status %d", resp.StatusCode)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return "", fmt.Errorf("teams channel: decode AAD token: %w", err)
	}
	if strings.TrimSpace(tok.AccessToken) == "" {
		return "", fmt.Errorf("teams channel: AAD token response carried no access_token")
	}
	s.token = tok.AccessToken
	s.tokenExp = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	return s.token, nil
}

func (s *connectorSender) send(ctx context.Context, serviceURL, conversationID string, act outboundActivity) (string, error) {
	base, err := activitiesURL(serviceURL, conversationID)
	if err != nil {
		return "", err
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := s.do(ctx, http.MethodPost, base, act, &out); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.ID), nil
}

func (s *connectorSender) edit(ctx context.Context, serviceURL, conversationID, activityID string, act outboundActivity) error {
	base, err := activitiesURL(serviceURL, conversationID)
	if err != nil {
		return err
	}
	id := strings.TrimSpace(activityID)
	if id == "" {
		return fmt.Errorf("teams channel: edit requires an activity id")
	}
	return s.do(ctx, http.MethodPut, base+"/"+url.PathEscape(id), act, nil)
}

// do issues one Connector request with the AAD bearer, encoding body as JSON
// and decoding a 2xx response into out (nil to discard).
func (s *connectorSender) do(ctx context.Context, method, endpoint string, body outboundActivity, out any) error {
	token, err := s.bearer(ctx)
	if err != nil {
		return err
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("teams channel: encode activity: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("teams channel: %s %s: %w", method, endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("teams channel: %s %s returned status %d", method, endpoint, resp.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// activitiesURL builds the Connector activities collection URL for a
// conversation, joining the per-conversation serviceUrl base with the
// url-escaped conversation id. An empty serviceUrl is a hard error (the runner
// failed to prime the conversation ref).
func activitiesURL(serviceURL, conversationID string) (string, error) {
	base := strings.TrimSpace(serviceURL)
	if base == "" {
		return "", fmt.Errorf("teams channel: no serviceUrl for conversation (ref not primed)")
	}
	conv := strings.TrimSpace(conversationID)
	if conv == "" {
		return "", fmt.Errorf("teams channel: no conversation id")
	}
	return strings.TrimRight(base, "/") + "/v3/conversations/" + url.PathEscape(conv) + "/activities", nil
}

// senderFor resolves the outbound credential and builds a sender via the
// injected factory. The resolver botID prefers the inbound-captured
// SourceAppID (the recipient bot in a multi-bot deployment), then TenantKey,
// then the channel's static appID. Per-call resolution keeps a rotated
// password hot.
func (c *Channel) senderFor(ctx context.Context, target channel.ReplyTarget) (teamsSender, error) {
	botID := strings.TrimSpace(target.SourceAppID)
	if botID == "" {
		botID = strings.TrimSpace(target.TenantKey)
	}
	if botID == "" {
		botID = c.appID
	}
	cred, err := c.creds.Resolve(ctx, botID)
	if err != nil {
		return nil, err
	}
	return c.newSender(cred), nil
}

// serviceURLFor reads the per-conversation serviceUrl from the ConversationStore
// keyed by the target's conversation id. Empty when the ref was never primed
// (activitiesURL then rejects the send with a clear error).
func (c *Channel) serviceURLFor(target channel.ReplyTarget) string {
	if c.convRefs == nil {
		return ""
	}
	ref, ok := c.convRefs.Get(strings.TrimSpace(target.ExternalChatID))
	if !ok {
		return ""
	}
	return strings.TrimSpace(ref.ServiceURL)
}

// cardContent decodes a rendered card payload into its fallback text and
// Adaptive Card content. An empty payload yields text-only (the Reply path).
func cardContent(card channel.Card) (string, json.RawMessage, error) {
	if len(card.Payload) == 0 {
		return "", nil, nil
	}
	var wm teamsWireMessage
	if err := json.Unmarshal(card.Payload, &wm); err != nil {
		return "", nil, fmt.Errorf("teams channel: decode card payload: %w", err)
	}
	return wm.Text, wm.Card, nil
}

// buildActivity assembles the outbound Activity from decoded card content. A
// non-empty card becomes an Adaptive Card attachment; text is always carried as
// the accessible fallback. replyToID threads the activity under the inbound
// message when set.
func buildActivity(text string, cardJSON json.RawMessage, replyToID string) outboundActivity {
	act := outboundActivity{
		Type:      "message",
		Text:      strings.TrimSpace(text),
		ReplyToID: strings.TrimSpace(replyToID),
	}
	if len(cardJSON) > 0 {
		act.Attachments = []attachment{{
			ContentType: adaptiveCardContentType,
			Content:     cardJSON,
		}}
	}
	return act
}

// Reply posts a plain-text command acknowledgement, threaded under the inbound
// message when the target carries a reply anchor.
func (c *Channel) Reply(ctx context.Context, target channel.ReplyTarget, text string) error {
	sender, err := c.senderFor(ctx, target)
	if err != nil {
		return err
	}
	act := buildActivity(text, nil, target.ReplyToMessageID)
	_, err = sender.send(ctx, c.serviceURLFor(target), target.ExternalChatID, act)
	return err
}

// Send posts a new Adaptive Card message and returns its activity id as the
// MessageRef id. This is the adapter's single interactive-send path.
func (c *Channel) Send(ctx context.Context, target channel.ReplyTarget, card channel.Card) (gateway.MessageRef, error) {
	sender, err := c.senderFor(ctx, target)
	if err != nil {
		return gateway.MessageRef{}, err
	}
	text, cardJSON, err := cardContent(card)
	if err != nil {
		return gateway.MessageRef{}, err
	}
	act := buildActivity(text, cardJSON, target.ReplyToMessageID)
	id, err := sender.send(ctx, c.serviceURLFor(target), target.ExternalChatID, act)
	if err != nil {
		return gateway.MessageRef{}, err
	}
	return gateway.MessageRef{ID: id, Text: text}, nil
}

// Edit PUTs an existing activity in place (the inflight "executing" → terminal
// transition). The conversation id comes from the ReplyTarget; the activity id
// from the MessageRef.
func (c *Channel) Edit(ctx context.Context, target channel.ReplyTarget, ref gateway.MessageRef, card channel.Card) error {
	sender, err := c.senderFor(ctx, target)
	if err != nil {
		return err
	}
	activityID := strings.TrimSpace(ref.ID)
	if activityID == "" {
		return fmt.Errorf("teams channel: edit requires an activity id")
	}
	text, cardJSON, err := cardContent(card)
	if err != nil {
		return err
	}
	act := buildActivity(text, cardJSON, "")
	return sender.edit(ctx, c.serviceURLFor(target), target.ExternalChatID, activityID, act)
}
