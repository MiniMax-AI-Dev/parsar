package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// FeishuRouter composes the store reads the Feishu inbound handler
// needs. The router does NOT depend on the store package — both
// directions would create an import cycle. Callers adapt Store methods
// to this interface with thin wrappers.
type FeishuRouter interface {
	// GetAgentByFeishuAppID returns the Agent route info for a Bot
	// App ID, or an error wrapping a sentinel for "unknown / disabled".
	GetAgentByFeishuAppID(ctx context.Context, appID string) (FeishuRouteAgent, error)

	// GetAgentByID returns route info for an active Agent chosen by a
	// shared Bot session. Caller still runs the visibility gate before
	// dispatching.
	GetAgentByID(ctx context.Context, agentID string) (FeishuRouteAgent, error)

	// FindUserIDByPlatformSubject resolves an inbound sender to a Parsar
	// user_id by (platform, subject). Returns ErrRouterUnknownUser for guests.
	FindUserIDByPlatformSubject(ctx context.Context, platform, subject string) (string, error)

	// IsActiveWorkspaceMember returns true when user_id is an active
	// member. False (with nil error) means "registered but not a
	// member" — the gate differentiates workspace from tenant/public.
	IsActiveWorkspaceMember(ctx context.Context, workspaceID, userID string) (bool, error)

	// GetWorkspaceVisibility returns "public" or "private". Used only
	// in the visibility=workspace rejection path to decide whether to
	// surface a "Request to join" link. Errors are swallowed by the caller.
	GetWorkspaceVisibility(ctx context.Context, workspaceID string) (string, error)

	// ListWorkspaceOwnerNames returns up to `limit` active-owner names
	// (earliest-joined first), used to render "Admins: A, B" inside
	// the rejection card. Errors are swallowed by the caller.
	ListWorkspaceOwnerNames(ctx context.Context, workspaceID string, limit int32) ([]string, error)
}

// FeishuRouteAgent is the projection FeishuRouter.GetAgentByFeishuAppID
// returns. The store wrapper converts its FeishuAgentRoute into this
// shape at the call site.
type FeishuRouteAgent struct {
	AgentID         string
	WorkspaceID     string
	WorkspaceName   string
	AgentName       string
	AgentSlug       string
	Visibility      Visibility
	Config          []byte
	CreatedByUserID string
}

// ErrFeishuRouterUnknownAgent is returned when no enabled Agent claims
// the Bot App ID. Normally translates into HTTP 400.
var ErrFeishuRouterUnknownAgent = errors.New("no agent registered for feishu app_id")

// ErrRouterUnknownUser signals the inbound sender is not a
// Parsar user (visibility gate treats them as guest).
var ErrRouterUnknownUser = errors.New("no parsar user linked to platform sender")

// FeishuConnectorConfig is the agents.config.connectors.feishu subtree.
// Secret material lives in vault via *_ref pointers, never plain text.
type FeishuConnectorConfig struct {
	Enabled              bool   `json:"enabled"`
	AppID                string `json:"app_id"`
	AppSecretRef         string `json:"app_secret_ref"`
	VerificationTokenRef string `json:"verification_token_ref"`
	EncryptKeyRef        string `json:"encrypt_key_ref"`
	BotOpenID            string `json:"bot_open_id"`
	EventMode            string `json:"event_mode"`
	RoutingMode          string `json:"routing_mode"`
}

// DecodeFeishuConnectorConfig extracts the connector subtree from raw
// agents.config jsonb. Returns ok=false (nil error) when the subtree
// is absent — that's a normal "not a Feishu Bot host" state.
func DecodeFeishuConnectorConfig(raw []byte) (cfg FeishuConnectorConfig, ok bool, err error) {
	if len(raw) == 0 {
		return FeishuConnectorConfig{}, false, nil
	}
	var envelope struct {
		Connectors struct {
			Feishu *FeishuConnectorConfig `json:"feishu"`
		} `json:"connectors"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return FeishuConnectorConfig{}, false, fmt.Errorf("decode agent.config connectors: %w", err)
	}
	if envelope.Connectors.Feishu == nil {
		return FeishuConnectorConfig{}, false, nil
	}
	return *envelope.Connectors.Feishu, true, nil
}

// FeishuInboundEvent is the minimal projection the router needs from a
// verified inbound Feishu event.
type FeishuInboundEvent struct {
	AppID          string
	MessageID      string
	RootID         string
	ParentID       string
	ChatID         string
	ChatType       string
	ThreadID       string
	MessageType    string
	RawContent     string
	Text           string
	SenderOpenID   string
	SenderUserID   string
	SenderUnionID  string
	SenderType     string
	TenantKey      string
	MentionOpenIDs []string
	MentionKeys    []string
	Metadata       map[string]any
}

// IsBotSender reports whether the inbound was produced by a Feishu
// app/bot (sender_type != "user"). Shared bot path uses it to bypass
// @-mention filtering.
func (e FeishuInboundEvent) IsBotSender() bool {
	t := strings.TrimSpace(strings.ToLower(e.SenderType))
	return t != "" && t != "user"
}

// ReplyAnchorMessageID returns the message_id Parsar should reply to.
// Always returns MessageID — never RootID/ParentID.
//
// Why: Feishu's POST /im/v1/messages/{message_id}/reply with a thread
// root + reply_in_thread=true fans out to N message_ids (one per
// existing reply), surfacing N visually-identical cards. The DB trace
// shows "1 outbound → 1 SendMessage → 1 om_id"; the duplication is
// invisible until viewing the chat.
func (e FeishuInboundEvent) ReplyAnchorMessageID() string {
	return strings.TrimSpace(e.MessageID)
}

// ThreadKey returns the identifier that groups every inbound from the
// same Feishu thread into one Parsar conversation. Picked
// from RootID then MessageID — NOT ThreadID.
//
// Why not ThreadID: Feishu's thread panel populates ThreadID with a
// separate identifier (e.g. omt_…) that has no overlap with the root's
// MessageID. Using it would split root and replies into two
// conversations. RootID is consistent: replies stamp the root's
// MessageID into RootID; the root itself has empty RootID and uses
// its own MessageID — both end up with the same key.
//
// Distinct from ReplyAnchorMessageID: that always returns MessageID
// because the reply API would otherwise fan out.
func (e FeishuInboundEvent) ThreadKey() string {
	if id := strings.TrimSpace(e.RootID); id != "" {
		return id
	}
	return strings.TrimSpace(e.MessageID)
}

// FeishuInboundDecision is what RouteFeishuInbound returns. Allowed=true
// means proceed; otherwise ReplyHint carries the bot reply to render
// back to Feishu and stop.
type FeishuInboundDecision struct {
	Agent          FeishuRouteAgent
	Decision       Decision
	SenderUserID   string // Parsar user_id, empty when not registered.
	SenderState    SenderState
	NormalizedText string
}

// NeutralFromFeishuEvent bridges a Feishu-typed inbound event to the neutral
// gateway.InboundEvent at the router boundary. It is the struct-level twin of
// the channel adapter's Normalize (which starts from raw webhook bytes): the
// N2 inbound manager still decodes the Lark SDK type into a FeishuInboundEvent,
// then calls this to feed the now-neutral routing core. Platform-rich fields
// the shared router persists as message metadata (message_type, raw_content,
// sender_type) are folded into ev.Metadata alongside the parser metadata
// (mention_keys, parsed image refs). Slated for removal once every caller
// produces the neutral event directly (N3).
func NeutralFromFeishuEvent(fe FeishuInboundEvent) InboundEvent {
	meta := make(map[string]any, len(fe.Metadata)+3)
	for k, v := range fe.Metadata {
		meta[k] = v
	}
	// These three live as typed fields on FeishuInboundEvent but have no
	// neutral slot; carry them in Metadata so sharedMetadata reproduces the
	// stored jsonb byte-for-byte (sender_type only surfaces on the bot path).
	meta["message_type"] = fe.MessageType
	meta["raw_content"] = fe.RawContent
	meta["sender_type"] = fe.SenderType
	return InboundEvent{
		Platform:          "feishu",
		BotID:             fe.AppID,
		ExternalMessageID: fe.MessageID,
		ExternalChatID:    fe.ChatID,
		ExternalThreadID:  fe.ThreadID,
		ExternalRootID:    fe.RootID,
		Sender: ExternalIdentity{
			PlatformUserID: fe.SenderUnionID,
			LocalUserID:    fe.SenderOpenID,
			TenantKey:      fe.TenantKey,
		},
		Text:             fe.Text,
		ChatType:         neutralChatTypeFromFeishu(fe.ChatType),
		SenderIsBot:      fe.IsBotSender(),
		MentionedUserIDs: fe.MentionOpenIDs,
		ReplyTo:          fe.ParentID,
		Metadata:         meta,
	}
}

// neutralChatTypeFromFeishu maps Feishu chat_type to the neutral vocabulary
// (p2p→dm, anything else non-empty→group). Mirrors the channel/feishu
// adapter helper of the same purpose; duplicated because gateway must not
// import the channel package (import cycle).
func neutralChatTypeFromFeishu(feishuChatType string) string {
	switch strings.ToLower(strings.TrimSpace(feishuChatType)) {
	case "":
		return ""
	case "p2p":
		return "dm"
	default:
		return "group"
	}
}

// RouteFeishuInbound is the Feishu adapter entry to the neutral routing
// core: bridge the Feishu-typed event and delegate to RouteInbound. Retained
// (as a thin shim) so the Feishu-typed gate tests keep exercising the gate
// end to end; production callers already speak neutral
// (RouteInbound / RouteInboundToAgent). Retire it with the Feishu-typed gate
// tests when the gateway Feishu* types are renamed neutral.
func RouteFeishuInbound(ctx context.Context, router FeishuRouter, event FeishuInboundEvent, cfg GateConfig) (FeishuInboundDecision, error) {
	return RouteInbound(ctx, router, NeutralFromFeishuEvent(event), cfg)
}

// RouteInbound stitches agent lookup (by ev.BotID), sender lookup, and
// visibility-gate evaluation on a neutral inbound event.
func RouteInbound(ctx context.Context, router FeishuRouter, event InboundEvent, cfg GateConfig) (FeishuInboundDecision, error) {
	appID := strings.TrimSpace(event.BotID)
	if appID == "" {
		return FeishuInboundDecision{}, fmt.Errorf("%w: empty app_id on inbound event", ErrFeishuRouterUnknownAgent)
	}

	agent, err := router.GetAgentByFeishuAppID(ctx, appID)
	if err != nil {
		return FeishuInboundDecision{}, err
	}
	return RouteInboundToAgent(ctx, router, event, agent, cfg)
}

// RouteInboundToAgent runs sender resolution and visibility-gate evaluation
// for a known Agent route on a neutral inbound event. Shared Bot command
// routers use this after resolving /select state.
func RouteInboundToAgent(ctx context.Context, router FeishuRouter, event InboundEvent, agent FeishuRouteAgent, cfg GateConfig) (FeishuInboundDecision, error) {
	// PlatformUserID is the cross-tenant stable id (Feishu union_id)
	// matching auth_identities.subject; fall back to the platform-local id
	// (Feishu open_id) only when absent (legacy event shapes).
	subject := strings.TrimSpace(event.Sender.PlatformUserID)
	if subject == "" {
		subject = strings.TrimSpace(event.Sender.LocalUserID)
	}

	var (
		senderUserID string
		registered   bool
	)
	if subject != "" {
		uid, err := router.FindUserIDByPlatformSubject(ctx, event.Platform, subject)
		switch {
		case err == nil:
			senderUserID = uid
			registered = true
		case errors.Is(err, ErrRouterUnknownUser):
			// unregistered — leave senderUserID empty.
		default:
			return FeishuInboundDecision{}, fmt.Errorf("router resolve sender: %w", err)
		}
	}

	var workspaceMember bool
	if registered {
		isMember, err := router.IsActiveWorkspaceMember(ctx, agent.WorkspaceID, senderUserID)
		if err != nil {
			return FeishuInboundDecision{}, fmt.Errorf("router workspace membership: %w", err)
		}
		workspaceMember = isMember
	}

	state := SenderState{Registered: registered, WorkspaceMember: workspaceMember}
	info := WorkspaceInfo{Name: agent.WorkspaceName}

	// Only fetch owner names + visibility when the gate is about to
	// deny on visibility=workspace. Errors are swallowed: the
	// rejection must still go out; the card degrades to the
	// "Contact an admin to request access" fallback.
	if agent.Visibility == VisibilityWorkspace && !workspaceMember {
		if cfg.JoinURLBuilder != nil {
			if vis, err := router.GetWorkspaceVisibility(ctx, agent.WorkspaceID); err == nil && vis == workspaceVisibilityPublic {
				info.JoinURL = cfg.JoinURLBuilder(agent.WorkspaceID)
			}
		}
		if owners, err := router.ListWorkspaceOwnerNames(ctx, agent.WorkspaceID, workspaceOwnerHintLimit); err == nil {
			info.OwnerNames = owners
		}
	}

	decision := VisibilityGate(agent.Visibility, state, info, cfg)

	return FeishuInboundDecision{
		Agent:          agent,
		Decision:       decision,
		SenderUserID:   senderUserID,
		SenderState:    state,
		NormalizedText: event.Text,
	}, nil
}

// workspaceVisibilityPublic mirrors the value stored in
// workspaces.visibility. Local copy avoids a gateway → store import
// cycle.
const workspaceVisibilityPublic = "public"

// workspaceOwnerHintLimit caps owner-name reads for the "Admins: A, B
// and N others" line. Capped at 5 to avoid unbounded reads on workspaces
// with hundreds of co-owners.
const workspaceOwnerHintLimit = 5

// FeishuInboundEventFromWebhook adapts the raw v2
// `im.message.receive_v1` body to a FeishuInboundEvent. Run signature
// verification BEFORE calling this.
func FeishuInboundEventFromWebhook(decodedBody []byte) (FeishuInboundEvent, error) {
	var envelope struct {
		Header struct {
			AppID string `json:"app_id"`
		} `json:"header"`
		Event struct {
			Message struct {
				MessageID   string `json:"message_id"`
				RootID      string `json:"root_id"`
				ParentID    string `json:"parent_id"`
				ChatID      string `json:"chat_id"`
				ChatType    string `json:"chat_type"`
				ThreadID    string `json:"thread_id"`
				MessageType string `json:"message_type"`
				Content     string `json:"content"`
				Mentions    []struct {
					Key string `json:"key"`
					ID  struct {
						OpenID string `json:"open_id"`
						UserID string `json:"user_id"`
					} `json:"id"`
				} `json:"mentions"`
			} `json:"message"`
			Sender struct {
				SenderID struct {
					OpenID  string `json:"open_id"`
					UserID  string `json:"user_id"`
					UnionID string `json:"union_id"`
				} `json:"sender_id"`
				SenderType string `json:"sender_type"`
				TenantKey  string `json:"tenant_key"`
			} `json:"sender"`
		} `json:"event"`
	}
	if err := json.Unmarshal(decodedBody, &envelope); err != nil {
		return FeishuInboundEvent{}, fmt.Errorf("decode feishu webhook: %w", err)
	}
	mentionOpenIDs := make([]string, 0, len(envelope.Event.Message.Mentions))
	mentionKeys := make([]string, 0, len(envelope.Event.Message.Mentions))
	for _, mention := range envelope.Event.Message.Mentions {
		if strings.TrimSpace(mention.ID.OpenID) != "" {
			mentionOpenIDs = append(mentionOpenIDs, strings.TrimSpace(mention.ID.OpenID))
		}
		if strings.TrimSpace(mention.Key) != "" {
			mentionKeys = append(mentionKeys, strings.TrimSpace(mention.Key))
		}
	}
	parsed := ParseFeishuMessageContent(envelope.Event.Message.MessageType, envelope.Event.Message.Content, mentionKeys)
	metadata := map[string]any{
		"mention_keys": mentionKeys,
	}
	for key, value := range parsed.Metadata {
		metadata[key] = value
	}
	return FeishuInboundEvent{
		AppID:          strings.TrimSpace(envelope.Header.AppID),
		MessageID:      envelope.Event.Message.MessageID,
		RootID:         envelope.Event.Message.RootID,
		ParentID:       envelope.Event.Message.ParentID,
		ChatID:         envelope.Event.Message.ChatID,
		ChatType:       envelope.Event.Message.ChatType,
		ThreadID:       envelope.Event.Message.ThreadID,
		MessageType:    envelope.Event.Message.MessageType,
		RawContent:     envelope.Event.Message.Content,
		Text:           parsed.Text,
		SenderOpenID:   envelope.Event.Sender.SenderID.OpenID,
		SenderUserID:   envelope.Event.Sender.SenderID.UserID,
		SenderUnionID:  envelope.Event.Sender.SenderID.UnionID,
		SenderType:     envelope.Event.Sender.SenderType,
		TenantKey:      envelope.Event.Sender.TenantKey,
		MentionOpenIDs: mentionOpenIDs,
		MentionKeys:    mentionKeys,
		Metadata:       metadata,
	}, nil
}
