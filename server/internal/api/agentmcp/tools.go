package agentmcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type askInput struct {
	Prompt string `json:"prompt" jsonschema:"Task or question for this Agent. Starts a new conversation; 1-32000 characters."`
}

type runInput struct {
	RunID string `json:"run_id" jsonschema:"Run ID returned by ask_agent."`
}

type runOutput struct {
	RunID            string `json:"run_id"`
	ConversationID   string `json:"conversation_id"`
	Status           string `json:"status"`
	Result           string `json:"result,omitempty"`
	Reason           string `json:"reason,omitempty"`
	ConversationPath string `json:"conversation_path"`
}

func (h *Handler) server(id store.AgentMCPIdentity) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "Parsar: " + id.Name, Version: "1.0.0"},
		&mcp.ServerOptions{Instructions: "Call ask_agent to start a task. Keep its run_id and call get_run until terminal. Pending approvals and questions are handled in the Parsar conversation. Results use the Agent's configured model, runtime and capabilities."})
	mcp.AddTool(s, &mcp.Tool{Name: "ask_agent", Description: "Ask " + id.Name + " to perform a task. " + id.Description + " Returns a run ID; use get_run to retrieve its answer."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in askInput) (*mcp.CallToolResult, runOutput, error) {
			prompt := strings.TrimSpace(in.Prompt)
			if prompt == "" || utf8.RuneCountInString(prompt) > 32000 {
				return nil, runOutput{}, errors.New("prompt must be 1-32000 characters")
			}
			conversation, err := h.store.CreateWorkspaceConversation(ctx, store.CreateWorkspaceConversationInput{
				WorkspaceID: id.WorkspaceID, PrimaryAgentID: id.AgentID, Surface: "api", Form: "oneshot",
				Title: "MCP · " + id.Name, Metadata: map[string]any{"source": "mcp"},
			})
			if err != nil {
				return nil, runOutput{}, h.toolError(err, "could not create conversation")
			}
			result, err := h.store.SendUserMessageToConversation(ctx, store.SendUserMessageToConversationInput{
				ConversationID: conversation.ID, UserID: id.UserID, Content: prompt, Source: "mcp", MentionedAgentIDs: []string{id.AgentID},
			})
			if err != nil {
				return nil, runOutput{}, h.toolError(err, "could not start Agent run")
			}
			if len(result.RunIDs) != 1 {
				return nil, runOutput{}, errors.New("Agent did not start; check its status in Parsar")
			}
			return nil, runOutput{RunID: result.RunIDs[0], ConversationID: conversation.ID, Status: "queued", ConversationPath: "/c/" + conversation.ID}, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "get_run", Description: "Retrieve the result of your task for this Agent. Waits up to 20 seconds. If still queued or running, call again; open the conversation in Parsar to answer any pending approvals or questions.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}},
		func(ctx context.Context, _ *mcp.CallToolRequest, in runInput) (*mcp.CallToolResult, runOutput, error) {
			if _, err := uuid.Parse(in.RunID); err != nil {
				return nil, runOutput{}, errors.New("invalid run_id")
			}
			deadline := time.NewTimer(20 * time.Second)
			defer deadline.Stop()
			tick := time.NewTicker(time.Second)
			defer tick.Stop()
			for {
				run, err := h.store.GetAgentRun(ctx, in.RunID)
				if err != nil {
					return nil, runOutput{}, h.toolError(err, "could not read Agent run")
				}
				if run.AgentID != id.AgentID || run.WorkspaceID != id.WorkspaceID || run.RequestedByType != "user" || run.RequestedByID != id.UserID {
					return nil, runOutput{}, errors.New("run not found")
				}
				out := runOutput{RunID: run.ID, ConversationID: run.ConversationID, Status: run.Status, Reason: run.UserFacingReason, ConversationPath: "/c/" + run.ConversationID}
				if run.OutputMessage != nil {
					out.Result = run.OutputMessage.Content
				}
				if run.Status != "queued" && run.Status != "running" {
					return nil, out, nil
				}
				select {
				case <-ctx.Done():
					return nil, runOutput{}, ctx.Err()
				case <-deadline.C:
					return nil, out, nil
				case <-tick.C:
				}
			}
		})
	return s
}

func (h *Handler) toolError(err error, message string) error {
	if errors.Is(err, store.ErrUnknownAgentRun) {
		return errors.New("run not found")
	}
	if errors.Is(err, store.ErrUnknownMention) {
		return errors.New("Agent is unavailable")
	}
	if h.logger != nil {
		h.logger.Error("agent MCP tool failed", "error", err)
	}
	return fmt.Errorf("%s; check Parsar for details", message)
}
