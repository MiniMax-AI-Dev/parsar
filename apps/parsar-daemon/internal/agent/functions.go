package agent

import (
	"context"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

var ErrUnknownFunctionCall = errors.New("agent: function call is no longer pending")

type FunctionResultSubmitter interface {
	SubmitFunctionResult(context.Context, proto.FunctionResultPayload) error
}
