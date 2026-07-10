package dev

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"net/http"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
)

type gatewayInboundRequest struct {
	Gateway           string              `json:"gateway"`
	Conversation      string              `json:"conversation"`
	Sender            string              `json:"sender"`
	Text              string              `json:"text"`
	ExternalChatID    string              `json:"external_chat_id"`
	ExternalUserID    string              `json:"external_user_id"`
	ExternalThreadID  string              `json:"external_thread_id"`
	ExternalMessageID string              `json:"external_message_id"`
	TargetAgentID     string              `json:"target_agent_id"`
	SourceAppID       string              `json:"source_app_id"`
	ConversationForm  string              `json:"conversation_form"`
	Message           gatewayMessage      `json:"message"`
	Actor             gatewayActor        `json:"actor"`
	ConversationRef   gatewayConversation `json:"conversation_ref"`
	Metadata          map[string]any      `json:"metadata"`
}

type gatewayMessage struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type gatewayActor struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

type gatewayConversation struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	ThreadID string `json:"thread_id"`
}

// createGatewayInbound writes an inbound gateway envelope to storage.
//
//	@Summary		Create an inbound gateway envelope
//	@Description	Writes an inbound gateway envelope from the connector daemon into storage for dispatch. Development helper.
//	@Tags			gateway
//	@ID				createDevGatewayInbound
//	@Accept			json
//	@Produce		json
//	@Param			body	body	gatewayInboundRequest	true	"Gateway inbound payload"
//	@Success		201 {object} map[string]interface{} "Created row"
//	@Failure		400 {object} map[string]string "Invalid body"
//	@Router			/dev/gateway/inbound [post]
func createGatewayInbound(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req gatewayInboundRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		createGatewayInboundFromRequest(w, r, runtimeStore, req)
	}
}

func createGatewayInboundFromRequest(w http.ResponseWriter, r *http.Request, runtimeStore RuntimeStore, req gatewayInboundRequest) {
	if strings.TrimSpace(req.Gateway) == "" {
		req.Gateway = "dev"
	}
	normalizeGatewayInbound(&req)
	if strings.TrimSpace(req.Conversation) == "" && strings.TrimSpace(req.ExternalChatID) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "conversation or external_chat_id is required"})
		return
	}
	if strings.TrimSpace(req.Sender) == "" && strings.TrimSpace(req.ExternalUserID) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "sender or external_user_id is required"})
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "text is required"})
		return
	}

	mentions := mentionPattern.FindAllString(req.Text, -1)
	if runtimeStore == nil {
		writeJSON(w, http.StatusCreated, map[string]any{
			"gateway":    req.Gateway,
			"message_id": fmt.Sprintf("gateway_msg_%d", time.Now().UnixNano()),
			"run_ids":    []string{},
			"mentions":   mentions,
			"created_at": time.Now().UTC(),
		})
		return
	}

	result, err := runtimeStore.CreateInboundIMMessage(r.Context(), store.CreateInboundIMMessageInput{
		ConversationTitle: req.Conversation,
		SenderEmail:       req.Sender,
		Text:              req.Text,
		Mentions:          mentions,
		Source:            "gateway",
		Gateway:           req.Gateway,
		ExternalUserID:    req.ExternalUserID,
		ExternalChatID:    req.ExternalChatID,
		ExternalThreadID:  req.ExternalThreadID,
		ExternalMessageID: req.ExternalMessageID,
		TargetAgentID:     req.TargetAgentID,
		SourceAppID:       req.SourceAppID,
		ConversationForm:  req.ConversationForm,
		Metadata:          req.Metadata,
	})
	if err != nil {
		if errors.Is(err, store.ErrUnknownMention) || errors.Is(err, store.ErrUnknownConversation) || errors.Is(err, store.ErrUnknownSender) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create gateway inbound message"})
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"gateway":    req.Gateway,
		"message_id": result.MessageID,
		"run_ids":    result.RunIDs,
		"mentions":   result.Mentions,
		"created_at": result.CreatedAt,
	})
}

func normalizeGatewayInbound(req *gatewayInboundRequest) {
	if strings.TrimSpace(req.Text) == "" {
		req.Text = req.Message.Text
	}
	if strings.TrimSpace(req.ExternalMessageID) == "" {
		req.ExternalMessageID = req.Message.ID
	}
	if strings.TrimSpace(req.Sender) == "" {
		req.Sender = req.Actor.Email
	}
	if strings.TrimSpace(req.ExternalUserID) == "" {
		req.ExternalUserID = req.Actor.ID
	}
	if strings.TrimSpace(req.Conversation) == "" {
		req.Conversation = req.ConversationRef.Title
	}
	if strings.TrimSpace(req.ExternalChatID) == "" {
		req.ExternalChatID = req.ConversationRef.ID
	}
	if strings.TrimSpace(req.ExternalThreadID) == "" {
		req.ExternalThreadID = req.ConversationRef.ThreadID
	}
}

type markGatewayOutboundDeliveredBody struct {
	DeliveryID string `json:"delivery_id"`
}

// listGatewayOutbound dumps a slice of the inflight-card driver state
// for ops / smoke tests. Each row carries conversation_id / agent_run_id
// so ops can correlate against agent_runs + conversations.
// listGatewayOutbound lists pending outbound gateway messages.
//
//	@Summary		List outbound gateway messages
//	@Description	Returns outbound gateway messages, optionally filtered by connector or delivery status.
//	@Tags			gateway
//	@ID				listDevGatewayOutbound
//	@Produce		json
//	@Success		200 {object} map[string]interface{} "Outbound message rows"
//	@Router			/dev/gateway/outbound [get]
func listGatewayOutbound(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed gateway outbound is disabled"})
			return
		}
		gateway := strings.TrimSpace(r.URL.Query().Get("gateway"))
		// inflightCutoffWindow is ~5m; debug surface uses a longer
		// look-back, with the limit capping response size.
		cutoff := time.Now().UTC().Add(-1 * time.Hour)
		convs, err := runtimeStore.ListActiveFeishuInflightConversations(r.Context(), cutoff, parseLimit(r, 100))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list gateway inflight conversations"})
			return
		}
		if gateway == "" {
			gateway = "feishu"
		}
		writeJSON(w, http.StatusOK, map[string]any{"gateway": gateway, "inflight": inflightDeliveries(convs)})
	}
}

func inflightDeliveries(convs []store.FeishuInflightConversation) []map[string]any {
	deliveries := make([]map[string]any, 0, len(convs))
	for _, c := range convs {
		row := map[string]any{
			"conversation_id":    c.ConversationID,
			"workspace_id":       c.WorkspaceID,
			"agent_run_id":       c.AgentRunID,
			"external_chat_id":   c.ExternalChatID,
			"external_thread_id": c.ExternalThreadID,
			"source_app_id":      c.SourceAppID,
			"run_status":         c.RunStatus,
			"max_seq":            c.MaxEventSequence,
		}
		// Pull the working slot (msg id / retry triad / seq_emitted)
		// out of conversation gateway_inflight metadata.
		if inflight, ok := c.ConversationMetadata["gateway_inflight"].(map[string]any); ok {
			if working, ok := inflight["working"].(map[string]any); ok {
				row["working_msg_id"] = working["external_msg_id"]
				row["seq_emitted"] = working["seq_emitted"]
				if v, ok := working["attempts"]; ok {
					row["working_attempts"] = v
				}
				if v, ok := working["last_error"]; ok {
					row["working_last_error"] = v
				}
				if v, ok := working["next_retry_at"]; ok {
					row["working_next_retry_at"] = v
				}
			}
		}
		deliveries = append(deliveries, row)
	}
	return deliveries
}

// markGatewayOutboundDelivered marks an outbound message as delivered.
//
//	@Summary		Mark an outbound gateway message delivered
//	@Description	Marks a gateway outbound message row as delivered. Used by the connector daemon to close the loop.
//	@Tags			gateway
//	@ID				markDevGatewayOutboundDelivered
//	@Accept			json
//	@Produce		json
//	@Param			messageID	path	string							true	"Message ID"
//	@Param			body		body	markGatewayOutboundDeliveredBody	true	"Delivery payload"
//	@Success		200 {object} map[string]interface{} "Updated row"
//	@Failure		400 {object} map[string]string "Invalid body or ID"
//	@Failure		404 {object} map[string]string "Message not found"
//	@Router			/dev/gateway/outbound/{messageID}/delivered [post]
func markGatewayOutboundDelivered(runtimeStore RuntimeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runtimeStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database-backed gateway outbound is disabled"})
			return
		}
		messageID := strings.TrimSpace(chi.URLParam(r, "messageID"))
		if !isUUID(messageID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "message_id must be a valid uuid"})
			return
		}
		var req markGatewayOutboundDeliveredBody
		if r.Body != nil {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
				return
			}
		}
		result, err := runtimeStore.MarkGatewayOutboundDelivered(r.Context(), store.MarkGatewayOutboundDeliveredInput{MessageID: messageID, DeliveryID: req.DeliveryID})
		if err != nil {
			if errors.Is(err, store.ErrUnknownMessage) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to mark gateway outbound delivered"})
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}
