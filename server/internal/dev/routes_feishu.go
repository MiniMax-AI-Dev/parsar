package dev

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"net/http"
	"os"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"

	authfeishu "github.com/MiniMax-AI-Dev/parsar/server/internal/auth/feishu"
	gatewaypkg "github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/router"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/secrets"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
)

// createFeishuMessageEvent receives Feishu event webhooks.
//
//	@Summary		Receive a Feishu event webhook
//	@Description	Receives inbound Feishu message-event webhooks. Handles URL verification challenge and event dispatch to the runtime.
//	@Tags			feishu
//	@ID				receiveFeishuMessageEvent
//	@Accept			json
//	@Produce		json
//	@Param			body	body	map[string]interface{}	true	"Feishu event envelope"
//	@Success		200 {object} map[string]interface{} "Acknowledged event"
//	@Failure		400 {object} map[string]string "Invalid event body or signature"
//	@Router			/api/v1/feishu/events/message [post]
func createFeishuMessageEvent(runtimeStore RuntimeStore, webhook feishuWebhookConfig, joinURLBuilder func(workspaceID string) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		if webhook.Enabled && !webhook.MockEnabled {
			decoded, isChallenge, challenge, err := verifyFeishuWebhookEvent(r.Context(), runtimeStore, body, webhook)
			if err != nil {
				switch {
				case errors.Is(err, authfeishu.ErrWebhookTokenMismatch):
					writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid feishu verification token"})
				case errors.Is(err, authfeishu.ErrWebhookDecryptFailed):
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failed to decrypt feishu event"})
				default:
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
				}
				return
			}
			if isChallenge {
				writeJSON(w, http.StatusOK, map[string]string{"challenge": challenge})
				return
			}
			body = decoded
		}
		event, err := gatewaypkg.FeishuInboundEventFromWebhook(body)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}

		// Tests and legacy dev shims may mount the Feishu endpoint without
		// a DB-backed store or may send the pre-v2 shape without header.app_id.
		// Keep the old normalization fallback there; real deployments go
		// through app_id -> Agent routing below.
		if runtimeStore == nil || strings.TrimSpace(event.AppID) == "" {
			var legacy gatewaypkg.FeishuMessageEvent
			if err := json.Unmarshal(body, &legacy); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
				return
			}
			inbound := gatewaypkg.NormalizeFeishuInbound(legacy)
			createGatewayInboundFromRequest(w, r, runtimeStore, gatewayInboundRequest{
				Gateway:         inbound.Gateway,
				Message:         gatewayMessage{ID: inbound.Message.ID, Text: inbound.Message.Text},
				Actor:           gatewayActor{ID: inbound.Actor.ID, Email: inbound.Actor.Email},
				ConversationRef: gatewayConversation{ID: inbound.ConversationRef.ID, Title: inbound.ConversationRef.Title, ThreadID: inbound.ConversationRef.ThreadID},
				Metadata:        inbound.Metadata,
			})
			return
		}

		route := feishuRuntimeRouter{store: runtimeStore}
		host, err := route.GetAgentByFeishuAppID(r.Context(), event.AppID)
		if err != nil {
			switch {
			case errors.Is(err, gatewaypkg.ErrFeishuRouterUnknownAgent):
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown feishu app_id"})
			default:
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to route feishu inbound"})
			}
			return
		}
		if isFeishuSelfMessage(host.Config, event.SenderOpenID) {
			writeJSON(w, http.StatusOK, map[string]any{
				"gateway":  "feishu",
				"accepted": false,
				"reason":   "bot_self_message",
			})
			return
		}
		if isFeishuGroupMessageWithoutBotMention(r.Context(), runtimeStore, host.Config, event) {
			writeJSON(w, http.StatusOK, map[string]any{
				"gateway":  "feishu",
				"accepted": false,
				"reason":   "group_without_bot_mention",
			})
			return
		}
		hostCfg, ok, err := gatewaypkg.DecodeFeishuConnectorConfig(host.Config)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to decode feishu connector"})
			return
		}
		if ok && router.IsSharedRoutingMode(hostCfg.RoutingMode) {
			reply := func(ctx context.Context, agent gatewaypkg.FeishuRouteAgent, _ gatewaypkg.InboundEvent, text string) error {
				return sendFeishuImmediateText(ctx, runtimeStore, agent, event, text)
			}
			outcome, err := router.HandleInbound(r.Context(), runtimeStore, host, gatewaypkg.NeutralFromFeishuEvent(event), reply, nil, gatewaypkg.GateConfig{JoinURLBuilder: joinURLBuilder})
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to handle shared feishu bot inbound"})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"gateway":  "feishu",
				"shared":   true,
				"accepted": outcome.Accepted,
				"replied":  outcome.Replied,
				"reason":   outcome.Reason,
				"agent_id": outcome.AgentID,
			})
			return
		}

		decision, err := gatewaypkg.RouteInboundToAgent(r.Context(), route, gatewaypkg.NeutralFromFeishuEvent(event), host, gatewaypkg.GateConfig{JoinURLBuilder: joinURLBuilder})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to route feishu inbound"})
			return
		}
		if !decision.Decision.Allowed {
			replied := false
			if decision.Decision.ReplyHint != "" {
				if err := sendFeishuImmediateText(r.Context(), runtimeStore, decision.Agent, event, decision.Decision.ReplyHint); err != nil {
					log.Bg().Warn("feishu inbound rejection reply failed", "app_id", event.AppID, "chat_id", event.ChatID, "err", err)
				} else {
					replied = true
				}
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"gateway":    "feishu",
				"accepted":   false,
				"replied":    replied,
				"reply_hint": decision.Decision.ReplyHint,
				"reason":     decision.Decision.Reason,
			})
			return
		}

		externalUserID := strings.TrimSpace(event.SenderUnionID)
		if externalUserID == "" {
			externalUserID = strings.TrimSpace(event.SenderOpenID)
		}
		conversationForm := "group"
		if strings.EqualFold(strings.TrimSpace(event.ChatType), "p2p") {
			conversationForm = "dm"
		}
		metadata := map[string]any{
			"chat_type":    event.ChatType,
			"tenant_key":   event.TenantKey,
			"sender_state": decision.SenderState,
			"message_type": event.MessageType,
			"raw_content":  event.RawContent,
			"root_id":      event.RootID,
			"parent_id":    event.ParentID,
			"thread_id":    event.ThreadID,
		}
		for key, value := range event.Metadata {
			if strings.TrimSpace(key) == "" || value == nil {
				continue
			}
			metadata[key] = value
		}
		if decision.Decision.GuestReplyHint != "" {
			metadata["guest_reply_hint"] = decision.Decision.GuestReplyHint
		}
		createGatewayInboundFromRequest(w, r, runtimeStore, gatewayInboundRequest{
			Gateway:          "feishu",
			Conversation:     router.ConversationTitle(decision.NormalizedText),
			ConversationForm: conversationForm,
			Text:             decision.NormalizedText,
			ExternalChatID:   event.ChatID,
			// ThreadKey (not ReplyAnchorMessageID): every inbound in
			// the same Feishu thread lands in the same Parsar
			// conversation. Mirrors gateway/router/router.go.
			ExternalThreadID:  event.ThreadKey(),
			ExternalMessageID: event.MessageID,
			ExternalUserID:    externalUserID,
			TargetAgentID:     decision.Agent.AgentID,
			SourceAppID:       event.AppID,
			Metadata:          metadata,
		})
	}
}

type feishuRuntimeRouter struct {
	store RuntimeStore
}

func (r feishuRuntimeRouter) GetAgentByFeishuAppID(ctx context.Context, appID string) (gatewaypkg.FeishuRouteAgent, error) {
	route, err := r.store.GetAgentByFeishuAppID(ctx, appID)
	if err != nil {
		if errors.Is(err, store.ErrUnknownFeishuAgent) {
			return gatewaypkg.FeishuRouteAgent{}, gatewaypkg.ErrFeishuRouterUnknownAgent
		}
		return gatewaypkg.FeishuRouteAgent{}, err
	}
	return gatewaypkg.FeishuRouteAgent{
		AgentID:       route.AgentID,
		WorkspaceID:   route.WorkspaceID,
		WorkspaceName: route.WorkspaceName,
		AgentName:     route.AgentName,
		AgentSlug:     route.AgentSlug,
		Visibility:    gatewaypkg.Visibility(route.Visibility),
		Config:        route.Config,
	}, nil
}

func (r feishuRuntimeRouter) GetAgentByID(ctx context.Context, agentID string) (gatewaypkg.FeishuRouteAgent, error) {
	route, err := r.store.GetAgentByID(ctx, agentID)
	if err != nil {
		if errors.Is(err, store.ErrUnknownFeishuAgent) {
			return gatewaypkg.FeishuRouteAgent{}, gatewaypkg.ErrFeishuRouterUnknownAgent
		}
		return gatewaypkg.FeishuRouteAgent{}, err
	}
	return gatewaypkg.FeishuRouteAgent{
		AgentID:       route.AgentID,
		WorkspaceID:   route.WorkspaceID,
		WorkspaceName: route.WorkspaceName,
		AgentName:     route.AgentName,
		AgentSlug:     route.AgentSlug,
		Visibility:    gatewaypkg.Visibility(route.Visibility),
		Config:        route.Config,
	}, nil
}

func (r feishuRuntimeRouter) FindUserIDByPlatformSubject(ctx context.Context, platform, subject string) (string, error) {
	userID, err := r.store.FindUserIDByPlatformSubject(ctx, platform, subject)
	if err != nil {
		if errors.Is(err, store.ErrUnknownPlatformUser) {
			return "", gatewaypkg.ErrRouterUnknownUser
		}
		return "", err
	}
	return userID, nil
}

func (r feishuRuntimeRouter) IsActiveWorkspaceMember(ctx context.Context, workspaceID, userID string) (bool, error) {
	return r.store.IsActiveWorkspaceMember(ctx, workspaceID, userID)
}

func (r feishuRuntimeRouter) GetWorkspaceVisibility(ctx context.Context, workspaceID string) (string, error) {
	return r.store.GetWorkspaceVisibility(ctx, workspaceID)
}

func (r feishuRuntimeRouter) ListWorkspaceOwnerNames(ctx context.Context, workspaceID string, limit int32) ([]string, error) {
	return r.store.ListActiveWorkspaceOwnerNames(ctx, workspaceID, limit)
}

func verifyFeishuWebhookEvent(ctx context.Context, runtimeStore RuntimeStore, body []byte, webhook feishuWebhookConfig) ([]byte, bool, string, error) {
	decoded, isChallenge, challenge, err := authfeishu.VerifyAndDecodeEvent(body, webhook.VerificationToken, webhook.EncryptKey)
	if err == nil || !errors.Is(err, authfeishu.ErrWebhookTokenMismatch) {
		return decoded, isChallenge, challenge, err
	}
	if runtimeStore == nil || feishuEnvelopeEncrypted(body) {
		return nil, false, "", err
	}
	event, parseErr := gatewaypkg.FeishuInboundEventFromWebhook(body)
	if parseErr != nil || strings.TrimSpace(event.AppID) == "" {
		return nil, false, "", err
	}
	route, routeErr := runtimeStore.GetAgentByFeishuAppID(ctx, event.AppID)
	if routeErr != nil {
		return nil, false, "", err
	}
	cfg, ok, cfgErr := gatewaypkg.DecodeFeishuConnectorConfig(route.Config)
	if cfgErr != nil || !ok || !cfg.Enabled || strings.TrimSpace(cfg.VerificationTokenRef) == "" {
		return nil, false, "", err
	}
	verifyToken, tokenErr := loadFeishuSecretString(ctx, runtimeStore, route.WorkspaceID, cfg.VerificationTokenRef, "verification_token", "token", "value", "api_key")
	if tokenErr != nil {
		return nil, false, "", err
	}
	return authfeishu.VerifyAndDecodeEvent(body, verifyToken, "")
}

func feishuEnvelopeEncrypted(body []byte) bool {
	var envelope struct {
		Encrypt string `json:"encrypt"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return false
	}
	return strings.TrimSpace(envelope.Encrypt) != ""
}

func isFeishuSelfMessage(rawConfig []byte, senderOpenID string) bool {
	senderOpenID = strings.TrimSpace(senderOpenID)
	if senderOpenID == "" {
		return false
	}
	cfg, ok, err := gatewaypkg.DecodeFeishuConnectorConfig(rawConfig)
	if err != nil || !ok {
		return false
	}
	return strings.TrimSpace(cfg.BotOpenID) != "" && strings.TrimSpace(cfg.BotOpenID) == senderOpenID
}

// feishuThreadHistoryLookup is the narrow store surface
// isFeishuGroupMessageWithoutBotMention needs to support thread follow-up.
// It is satisfied by RuntimeStore (production) and by the
// feishuSecretRouteStore test double.
type feishuThreadHistoryLookup interface {
	HasFeishuThreadInboundHistory(ctx context.Context, externalChatID, threadID string) (bool, error)
}

// isFeishuGroupMessageWithoutBotMention decides whether a group-chat
// inbound should be silently dropped before any routing / storage work.
//
// Decision order (true = drop, false = let it through):
//  1. p2p chat → false.
//  2. mentions present: include bot_open_id → false; else → true.
//  3. no mentions in a group: if (chat_id, thread_id) has prior bot
//     history → false (thread follow-up doesn't need re-@); else → true.
//
// bot_open_id missing → bot defaults to refusing all group messages;
// operator must configure via the connector panel or provisioning.
func isFeishuGroupMessageWithoutBotMention(ctx context.Context, store feishuThreadHistoryLookup, rawConfig []byte, event gatewaypkg.FeishuInboundEvent) bool {
	chatType := strings.ToLower(strings.TrimSpace(event.ChatType))
	if chatType == "p2p" || chatType == "" {
		return false
	}
	// Other Feishu apps/bots post interactive cards whose "@bot" text
	// lives in the card body, never in message.mentions. Treat any
	// non-user sender as already-targeted at us.
	if event.IsBotSender() {
		return false
	}
	botOpenID := ""
	if cfg, ok, err := gatewaypkg.DecodeFeishuConnectorConfig(rawConfig); err == nil && ok {
		botOpenID = strings.TrimSpace(cfg.BotOpenID)
	}
	// Step 2 — mentions present.
	if len(event.MentionOpenIDs) > 0 {
		if botOpenID == "" {
			return true
		}
		for _, mentionedOpenID := range event.MentionOpenIDs {
			if strings.TrimSpace(mentionedOpenID) == botOpenID {
				return false
			}
		}
		// Mentions exist but bot is not among them — message is aimed
		// at another participant, do not respond.
		return true
	}
	// Step 3 — no mentions in a group. Check thread participation via
	// ThreadKey (thread_id → root_id → message_id fallback). ThreadKey
	// == MessageID for non-thread inbounds; brand-new top-level
	// messages have no conversation yet, so that branch is a no-op.
	threadKey := strings.TrimSpace(event.ThreadKey())
	if threadKey != "" && store != nil {
		hasHistory, err := store.HasFeishuThreadInboundHistory(ctx, strings.TrimSpace(event.ChatID), threadKey)
		if err == nil && hasHistory {
			return false
		}
		// Fail closed on lookup error: drop. The next @mention recovers.
	}
	return true
}

func sendFeishuImmediateText(ctx context.Context, runtimeStore RuntimeStore, agent gatewaypkg.FeishuRouteAgent, event gatewaypkg.FeishuInboundEvent, text string) error {
	if runtimeStore == nil {
		return errors.New("runtime store is not configured")
	}
	cfg, ok, err := gatewaypkg.DecodeFeishuConnectorConfig(agent.Config)
	if err != nil {
		return err
	}
	if !ok || !cfg.Enabled || strings.TrimSpace(cfg.AppSecretRef) == "" {
		return errors.New("feishu connector missing app_secret_ref")
	}
	appSecret, err := loadFeishuSecretString(ctx, runtimeStore, agent.WorkspaceID, cfg.AppSecretRef, "app_secret", "secret", "value", "api_key")
	if err != nil {
		return err
	}
	content, err := gatewaypkg.BuildFeishuInteractiveContent(text)
	if err != nil {
		return err
	}
	client, err := gatewaypkg.NewFeishuTenantClient(gatewaypkg.FeishuTenantClientOptions{
		AppID:   cfg.AppID,
		BaseURL: strings.TrimSpace(os.Getenv("PARSAR_FEISHU_OPENAPI_BASE_URL")),
	})
	if err != nil {
		return err
	}
	sendCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if replyAnchor := event.ReplyAnchorMessageID(); replyAnchor != "" {
		_, err = client.ReplyMessage(sendCtx, appSecret, replyAnchor, gatewaypkg.FeishuMessageReplyRequest{
			MsgType:       "interactive",
			Content:       content,
			ReplyInThread: true,
		})
		return err
	}
	chatID := strings.TrimSpace(event.ChatID)
	if chatID == "" {
		return errors.New("feishu inbound missing chat_id for immediate reply")
	}
	_, err = client.SendMessage(sendCtx, appSecret, gatewaypkg.FeishuMessageSendRequest{
		ReceiveIDType: "chat_id",
		ReceiveID:     chatID,
		MsgType:       "interactive",
		Content:       content,
	})
	return err
}

func loadFeishuSecretString(ctx context.Context, runtimeStore RuntimeStore, workspaceID, secretID string, keys ...string) (string, error) {
	secretID = strings.TrimSpace(secretID)
	if secretID == "" {
		return "", errors.New("secret id is required")
	}
	payload, err := runtimeStore.GetSecretPayload(ctx, workspaceID, secretID)
	if err != nil {
		return "", err
	}
	masterKey := strings.TrimSpace(os.Getenv("PARSAR_MASTER_KEY"))
	if masterKey == "" {
		return "", errors.New("PARSAR_MASTER_KEY env not set")
	}
	secretService, err := secrets.New(masterKey)
	if err != nil {
		return "", err
	}
	decoded, err := secretService.Decrypt(payload.EncryptedPayload)
	if err != nil {
		return "", err
	}
	for _, key := range keys {
		if raw, ok := decoded[key].(string); ok && strings.TrimSpace(raw) != "" {
			return strings.TrimSpace(raw), nil
		}
	}
	return "", fmt.Errorf("secret %s payload missing expected string field", secretID)
}

// updateAgentFeishuConnectorBody is the request body for
// PATCH /api/v1/agents/{agentID}/connector/feishu.
type updateAgentFeishuConnectorBody struct {
	Enabled              bool   `json:"enabled"`
	AppID                string `json:"app_id"`
	AppSecretRef         string `json:"app_secret_ref"`
	VerificationTokenRef string `json:"verification_token_ref"`
	EncryptKeyRef        string `json:"encrypt_key_ref"`
	BotOpenID            string `json:"bot_open_id"`
	EventMode            string `json:"event_mode"`
	RoutingMode          string `json:"routing_mode"`
}

type pollAgentFeishuProvisioningBody struct {
	DeviceCode  string `json:"device_code"`
	IntervalSec int    `json:"interval_sec"`
	TenantBrand string `json:"tenant_brand"`
}

type feishuProvisioningResponse struct {
	Status          string                                       `json:"status"`
	Begin           *gatewaypkg.FeishuAppRegistrationBeginResult `json:"begin,omitempty"`
	NextIntervalSec int                                          `json:"next_interval_sec,omitempty"`
	Error           string                                       `json:"error,omitempty"`
	Description     string                                       `json:"description,omitempty"`
	AppID           string                                       `json:"app_id,omitempty"`
	AppSecretRef    string                                       `json:"app_secret_ref,omitempty"`
	BotOpenID       string                                       `json:"bot_open_id,omitempty"`
	BotName         string                                       `json:"bot_name,omitempty"`
	FeishuConnector *store.AgentFeishuConnectorChange            `json:"feishu_connector,omitempty"`
}

// getAgentFeishuConnectorDiagnostics returns a read-only Feishu Bot
// observation snapshot for admins and workspace members. Unlike the
// write path, this is a read/debug surface and never exposes secret refs.
// getAgentFeishuConnectorDiagnostics returns Feishu connector health.
//
//	@Summary		Get an agent's Feishu connector diagnostics
//	@Description	Returns diagnostic snapshots for the agent's Feishu connector (webhook subscription, event verify, bot token).
//	@Tags			feishu
//	@ID				getDevAgentFeishuDiagnostics
//	@Produce		json
//	@Param			agentID	path	string	true	"Agent UUID"
//	@Success		200 {object} map[string]interface{} "Diagnostics snapshot"
//	@Failure		400 {object} map[string]string "Invalid UUID"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Failure		404 {object} map[string]string "Agent not found"
//	@Router			/api/v1/agents/{agentID}/connector/feishu/diagnostics [get]
func getAgentFeishuConnectorDiagnostics(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentID := strings.TrimSpace(chi.URLParam(r, "agentID"))
		if runtimeStore == nil || !isUUID(agentID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_id must be a valid uuid"})
			return
		}
		agent, err := runtimeStore.GetAgent(r.Context(), agentID)
		if err != nil {
			writeStoreAgentError(w, err)
			return
		}
		if err := requireWorkspaceMember(r, runtimeStore, agent.WorkspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		diagnostics, err := runtimeStore.GetFeishuConnectorDiagnostics(r.Context(), agentID)
		if err != nil {
			writeStoreAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"diagnostics": diagnostics})
	}
}

// updateAgentFeishuConnector binds (or rebinds) an Agent to a Feishu
// Bot self-built app — writes agents.config.connectors.feishu so the
// inbound router and outbound worker can resolve this Agent.
// RBAC: workspace owner / admin (a misconfigured Bot can leak the
// workspace to the internet).
// updateAgentFeishuConnector updates the agent's Feishu connector config.
//
//	@Summary		Update an agent's Feishu connector
//	@Description	Updates the agent's Feishu connector credentials/config. Owner/admin only.
//	@Tags			feishu
//	@ID				updateDevAgentFeishuConnector
//	@Accept			json
//	@Produce		json
//	@Param			agentID	path	string							true	"Agent UUID"
//	@Param			body	body	updateAgentFeishuConnectorBody	true	"Connector payload"
//	@Success		200 {object} map[string]interface{} "Updated agent"
//	@Failure		400 {object} map[string]string "Invalid body or UUID"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Failure		404 {object} map[string]string "Agent not found"
//	@Router			/api/v1/agents/{agentID}/connector/feishu [patch]
func updateAgentFeishuConnector(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		agentID := strings.TrimSpace(chi.URLParam(r, "agentID"))
		if runtimeStore == nil || !isUUID(agentID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_id must be a valid uuid"})
			return
		}
		agent, err := runtimeStore.GetAgent(r.Context(), agentID)
		if err != nil {
			writeStoreAgentError(w, err)
			return
		}
		if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, agent.WorkspaceID); err != nil {
			writeRBACError(w, err)
			return
		}
		var req updateAgentFeishuConnectorBody
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		botOpenID, err := resolveFeishuBotOpenID(r.Context(), runtimeStore, agent.WorkspaceID, req.AppID, req.AppSecretRef, req.BotOpenID)
		if err != nil {
			log.Bg().Warn("feishu bot open_id auto-resolve failed", "agent_id", agentID, "app_id", req.AppID, "err", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "feishu_bot_open_id_resolve_failed", "detail": err.Error()})
			return
		}
		change, err := runtimeStore.UpdateAgentFeishuConnector(r.Context(), store.UpdateAgentFeishuConnectorInput{
			AgentID:              agentID,
			Enabled:              req.Enabled,
			AppID:                req.AppID,
			AppSecretRef:         req.AppSecretRef,
			VerificationTokenRef: req.VerificationTokenRef,
			EncryptKeyRef:        req.EncryptKeyRef,
			BotOpenID:            botOpenID,
			EventMode:            req.EventMode,
			RoutingMode:          req.RoutingMode,
		}, actorIDFromRequest(r))
		if err != nil {
			switch {
			case errors.Is(err, store.ErrFeishuConnectorIncomplete):
				// api-client.ts copies the JSON `error` field into
				// both envelope.code and .message on the frontend, so
				// the discriminator lives in `error`. `detail` carries
				// human-readable text for logs/devtools.
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
					"error":  "feishu_connector_incomplete",
					"detail": err.Error(),
				})
				return
			case errors.Is(err, store.ErrFeishuAppIDInUse):
				writeJSON(w, http.StatusConflict, map[string]string{
					"error":  "feishu_app_id_in_use",
					"detail": err.Error(),
				})
				return
			}
			writeStoreAgentError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"feishu_connector": change})
	}
}

// beginWorkspaceFeishuProvisioning starts the async Feishu app provisioning
// for the workspace's shared Feishu connector.
func beginWorkspaceFeishuProvisioning(cfg feishuRegistrationConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg.Client == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "feishu_app_registration_not_configured"})
			return
		}
		begin, err := cfg.Client.Begin(r.Context())
		if err != nil {
			log.Bg().Warn("workspace feishu app registration begin failed", "err", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "feishu_app_registration_begin_failed"})
			return
		}
		writeJSON(w, http.StatusOK, feishuProvisioningResponse{Status: "pending", Begin: &begin, NextIntervalSec: begin.Interval})
	}
}

// pollWorkspaceFeishuProvisioning completes the QR flow and stores a
// WebSocket-mode connector directly on the workspace. The returned app secret
// is encrypted with PARSAR_MASTER_KEY before the connector row is written.
func pollWorkspaceFeishuProvisioning(runtimeStore RuntimeStore, cfg feishuRegistrationConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg.Client == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "feishu_app_registration_not_configured"})
			return
		}
		workspaceID := strings.TrimSpace(chi.URLParam(r, "workspaceID"))
		var req pollAgentFeishuProvisioningBody
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if strings.TrimSpace(req.DeviceCode) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "device_code is required"})
			return
		}
		status, err := cfg.Client.Poll(r.Context(), req.DeviceCode, req.IntervalSec, req.TenantBrand)
		if err != nil {
			log.Bg().Warn("workspace feishu app registration poll failed", "workspace_id", workspaceID, "err", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "feishu_app_registration_poll_failed"})
			return
		}
		switch status.Kind {
		case gatewaypkg.FeishuAppRegistrationPollPending:
			writeJSON(w, http.StatusOK, feishuProvisioningResponse{Status: "pending", NextIntervalSec: status.NextIntervalSec})
			return
		case gatewaypkg.FeishuAppRegistrationPollError:
			writeJSON(w, http.StatusOK, feishuProvisioningResponse{Status: "error", Error: status.Error, Description: status.Description})
			return
		case gatewaypkg.FeishuAppRegistrationPollSuccess:
			// Continue below.
		default:
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "feishu_app_registration_unknown_status"})
			return
		}

		appID := strings.TrimSpace(status.ClientID)
		appSecret := strings.TrimSpace(status.ClientSecret)
		if appID == "" || appSecret == "" {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "feishu_app_registration_missing_credentials"})
			return
		}
		botInfo, err := validateProvisionedFeishuBot(r.Context(), appID, appSecret, feishuOpenAPIBaseURL(cfg.OpenAPIBaseURL))
		if err != nil {
			log.Bg().Warn("workspace feishu provisioned bot validation failed", "workspace_id", workspaceID, "app_id", appID, "err", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "feishu_bot_validation_failed"})
			return
		}
		secret, err := createFeishuAppSecretFromProvisioning(r.Context(), runtimeStore, workspaceID, "Workspace", appID, appSecret, actorIDFromRequest(r))
		if err != nil {
			log.Bg().Warn("workspace feishu provisioned app secret write failed", "workspace_id", workspaceID, "app_id", appID, "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed_to_store_feishu_app_secret"})
			return
		}
		_, err = runtimeStore.UpsertWorkspaceFeishuConnector(r.Context(), store.UpsertWorkspaceFeishuConnectorInput{
			WorkspaceID:  workspaceID,
			Enabled:      true,
			AppID:        appID,
			AppSecretRef: secret.ID,
			BotOpenID:    botInfo.OpenID,
			EventMode:    "websocket",
		}, actorIDFromRequest(r))
		if err != nil {
			switch {
			case errors.Is(err, store.ErrFeishuAppIDInUse):
				writeJSON(w, http.StatusConflict, map[string]string{"error": "feishu_app_id_in_use", "detail": err.Error()})
			case errors.Is(err, store.ErrFeishuConnectorIncomplete):
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "feishu_connector_incomplete", "detail": err.Error()})
			default:
				writeStoreAgentError(w, err)
			}
			return
		}
		writeJSON(w, http.StatusOK, feishuProvisioningResponse{
			Status:       "success",
			AppID:        appID,
			AppSecretRef: secret.ID,
			BotOpenID:    botInfo.OpenID,
			BotName:      botInfo.AppName,
		})
	}
}

// beginAgentFeishuProvisioning starts the async Feishu app provisioning.
//
//	@Summary		Begin Feishu provisioning
//	@Description	Starts the async Feishu app provisioning flow for the agent's connector. Returns a session handle to poll.
//	@Tags			feishu
//	@ID				beginDevAgentFeishuProvisioning
//	@Produce		json
//	@Param			agentID	path	string	true	"Agent UUID"
//	@Success		200 {object} map[string]interface{} "Provisioning session"
//	@Failure		400 {object} map[string]string "Invalid UUID"
//	@Failure		403 {object} map[string]string "Caller is not workspace owner/admin"
//	@Failure		404 {object} map[string]string "Agent not found"
//	@Router			/api/v1/agents/{agentID}/connector/feishu/provision/begin [post]
func beginAgentFeishuProvisioning(runtimeStore RuntimeStore, cfg feishuRegistrationConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg.Client == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "feishu_app_registration_not_configured"})
			return
		}
		if _, ok := agentForFeishuConnectorWrite(w, r, runtimeStore); !ok {
			return
		}
		begin, err := cfg.Client.Begin(r.Context())
		if err != nil {
			log.Bg().Warn("feishu app registration begin failed", "err", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "feishu_app_registration_begin_failed"})
			return
		}
		writeJSON(w, http.StatusOK, feishuProvisioningResponse{Status: "pending", Begin: &begin, NextIntervalSec: begin.Interval})
	}
}

// pollAgentFeishuProvisioning polls the async Feishu provisioning flow.
//
//	@Summary		Poll Feishu provisioning status
//	@Description	Polls the async Feishu provisioning flow. Returns the current step and any completion payload.
//	@Tags			feishu
//	@ID				pollDevAgentFeishuProvisioning
//	@Accept			json
//	@Produce		json
//	@Param			agentID	path	string							true	"Agent UUID"
//	@Param			body	body	pollAgentFeishuProvisioningBody	true	"Poll payload"
//	@Success		200 {object} map[string]interface{} "Provisioning status"
//	@Failure		400 {object} map[string]string "Invalid body or UUID"
//	@Failure		404 {object} map[string]string "Provisioning session not found"
//	@Router			/api/v1/agents/{agentID}/connector/feishu/provision/poll [post]
func pollAgentFeishuProvisioning(runtimeStore RuntimeStore, cfg feishuRegistrationConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cfg.Client == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "feishu_app_registration_not_configured"})
			return
		}
		agent, ok := agentForFeishuConnectorWrite(w, r, runtimeStore)
		if !ok {
			return
		}
		var req pollAgentFeishuProvisioningBody
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if strings.TrimSpace(req.DeviceCode) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "device_code is required"})
			return
		}
		status, err := cfg.Client.Poll(r.Context(), req.DeviceCode, req.IntervalSec, req.TenantBrand)
		if err != nil {
			log.Bg().Warn("feishu app registration poll failed", "err", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "feishu_app_registration_poll_failed"})
			return
		}
		switch status.Kind {
		case gatewaypkg.FeishuAppRegistrationPollPending:
			writeJSON(w, http.StatusOK, feishuProvisioningResponse{Status: "pending", NextIntervalSec: status.NextIntervalSec})
			return
		case gatewaypkg.FeishuAppRegistrationPollError:
			writeJSON(w, http.StatusOK, feishuProvisioningResponse{Status: "error", Error: status.Error, Description: status.Description})
			return
		case gatewaypkg.FeishuAppRegistrationPollSuccess:
			// proceed below
		default:
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "feishu_app_registration_unknown_status"})
			return
		}

		appID := strings.TrimSpace(status.ClientID)
		appSecret := strings.TrimSpace(status.ClientSecret)
		if appID == "" || appSecret == "" {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "feishu_app_registration_missing_credentials"})
			return
		}
		if ok := assertFeishuAppIDAvailableForAgent(w, r.Context(), runtimeStore, appID, agent.ID); !ok {
			return
		}

		botInfo, err := validateProvisionedFeishuBot(r.Context(), appID, appSecret, feishuOpenAPIBaseURL(cfg.OpenAPIBaseURL))
		if err != nil {
			log.Bg().Warn("feishu provisioned bot validation failed", "app_id", appID, "err", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "feishu_bot_validation_failed"})
			return
		}
		secret, err := createFeishuAppSecretFromProvisioning(r.Context(), runtimeStore, agent.WorkspaceID, agent.Name, appID, appSecret, actorIDFromRequest(r))
		if err != nil {
			log.Bg().Warn("feishu provisioned app secret write failed", "app_id", appID, "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed_to_store_feishu_app_secret"})
			return
		}
		change, err := runtimeStore.UpdateAgentFeishuConnector(r.Context(), store.UpdateAgentFeishuConnectorInput{
			AgentID:      agent.ID,
			Enabled:      true,
			AppID:        appID,
			AppSecretRef: secret.ID,
			BotOpenID:    botInfo.OpenID,
			EventMode:    "websocket",
		}, actorIDFromRequest(r))
		if err != nil {
			switch {
			case errors.Is(err, store.ErrFeishuAppIDInUse):
				writeJSON(w, http.StatusConflict, map[string]string{"error": "feishu_app_id_in_use", "detail": err.Error()})
			case errors.Is(err, store.ErrFeishuConnectorIncomplete):
				writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "feishu_connector_incomplete", "detail": err.Error()})
			default:
				writeStoreAgentError(w, err)
			}
			return
		}
		writeJSON(w, http.StatusOK, feishuProvisioningResponse{
			Status:          "success",
			AppID:           appID,
			AppSecretRef:    secret.ID,
			BotOpenID:       botInfo.OpenID,
			BotName:         botInfo.AppName,
			FeishuConnector: &change,
		})
	}
}

func agentForFeishuConnectorWrite(w http.ResponseWriter, r *http.Request, runtimeStore RuntimeStore) (store.AgentSummary, bool) {
	agentID := strings.TrimSpace(chi.URLParam(r, "agentID"))
	if runtimeStore == nil || !isUUID(agentID) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "agent_id must be a valid uuid"})
		return store.AgentSummary{}, false
	}
	agent, err := runtimeStore.GetAgent(r.Context(), agentID)
	if err != nil {
		writeStoreAgentError(w, err)
		return store.AgentSummary{}, false
	}
	if err := requireWorkspaceOwnerOrAdmin(r, runtimeStore, agent.WorkspaceID); err != nil {
		writeRBACError(w, err)
		return store.AgentSummary{}, false
	}
	return agent, true
}

func assertFeishuAppIDAvailableForAgent(w http.ResponseWriter, ctx context.Context, runtimeStore RuntimeStore, appID, agentID string) bool {
	existing, err := runtimeStore.GetAgentByFeishuAppID(ctx, appID)
	switch {
	case err == nil:
		if existing.AgentID != agentID {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "feishu_app_id_in_use"})
			return false
		}
	case errors.Is(err, store.ErrUnknownFeishuAgent):
		return true
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed_to_check_feishu_app_id"})
		return false
	}
	return true
}

func validateProvisionedFeishuBot(ctx context.Context, appID, appSecret, openAPIBaseURL string) (gatewaypkg.FeishuBotInfo, error) {
	client, err := gatewaypkg.NewFeishuTenantClient(gatewaypkg.FeishuTenantClientOptions{
		AppID:   appID,
		BaseURL: openAPIBaseURL,
	})
	if err != nil {
		return gatewaypkg.FeishuBotInfo{}, err
	}
	validateCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return client.BotInfo(validateCtx, appSecret)
}

// resolveFeishuBotOpenID fills in the bot's open_id when the caller left it
// blank. The value is derivable from the app credentials (bot/v3/info returns
// it), so the bind form no longer needs it as a manual field. When botOpenID is
// already set it is returned untouched; when app_id or app_secret_ref is absent
// there is nothing to derive from, so the blank is passed through and the store's
// own completeness check decides whether that is acceptable.
func resolveFeishuBotOpenID(ctx context.Context, runtimeStore RuntimeStore, workspaceID, appID, appSecretRef, botOpenID string) (string, error) {
	botOpenID = strings.TrimSpace(botOpenID)
	if botOpenID != "" {
		return botOpenID, nil
	}
	appID = strings.TrimSpace(appID)
	appSecretRef = strings.TrimSpace(appSecretRef)
	if appID == "" || appSecretRef == "" {
		return "", nil
	}
	appSecret, err := loadFeishuSecretString(ctx, runtimeStore, workspaceID, appSecretRef, "app_secret", "secret", "value", "api_key")
	if err != nil {
		return "", err
	}
	info, err := validateProvisionedFeishuBot(ctx, appID, appSecret, feishuOpenAPIBaseURL(""))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(info.OpenID), nil
}

func createFeishuAppSecretFromProvisioning(ctx context.Context, runtimeStore RuntimeStore, workspaceID, agentName, appID, appSecret, actorID string) (store.SecretRead, error) {
	masterKey := strings.TrimSpace(os.Getenv("PARSAR_MASTER_KEY"))
	if masterKey == "" {
		return store.SecretRead{}, errors.New("PARSAR_MASTER_KEY env not set")
	}
	secretService, err := secrets.New(masterKey)
	if err != nil {
		return store.SecretRead{}, err
	}
	payload := map[string]any{
		"app_id":     appID,
		"app_secret": appSecret,
		"source":     "feishu_qr_provisioning",
	}
	encrypted, err := secretService.Encrypt(payload)
	if err != nil {
		return store.SecretRead{}, err
	}
	name := "Feishu Bot App Secret"
	if strings.TrimSpace(agentName) != "" {
		name = fmt.Sprintf("%s Feishu Bot App Secret", strings.TrimSpace(agentName))
	}
	return runtimeStore.CreateSecret(ctx, store.CreateSecretInput{
		WorkspaceID: workspaceID,
		Name:        name,
		Kind:        "feishu_app_secret",
		Provider:    "feishu",
		AuthType:    "app_secret",
		Payload:     payload,
		Masked:      secrets.MaskPayload(payload),
		CreatedBy:   actorID,
	}, encrypted)
}

func feishuOpenAPIBaseURL(configured string) string {
	if strings.TrimSpace(configured) != "" {
		return strings.TrimSpace(configured)
	}
	if v := strings.TrimSpace(os.Getenv("PARSAR_FEISHU_OPENAPI_BASE_URL")); v != "" {
		return v
	}
	return strings.TrimSpace(os.Getenv(authfeishu.EnvAPIBase))
}
