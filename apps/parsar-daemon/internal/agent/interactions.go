package agent

import (
	"context"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// PermissionResponder optionally accepts decisions for emitted permission requests.
// Unknown or expired requests return ErrUnknownPermission.
type PermissionResponder interface {
	SubmitPermission(context.Context, string, proto.PermissionDecisionPayload) error
}

// UserChoiceResponder optionally accepts answers for emitted user-choice requests.
// Unknown or expired requests return ErrUnknownAsk.
type UserChoiceResponder interface {
	SubmitPromptForUserChoice(context.Context, string, proto.PromptForUserChoiceDecisionPayload) error
}
