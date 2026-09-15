package dispatch

import (
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func validateExecutionEnvironment(req proto.PromptRequestPayload, caps proto.AgentKindCapabilities) error {
	if req.OutputSchema != nil && !caps.OutputSchema {
		return errors.New("engine does not support output schemas")
	}
	if err := proto.ValidateOutputSchema(req.OutputSchema); err != nil {
		return err
	}
	if req.RemoteEnvironment != nil {
		if req.DisableExecutionEnvironment {
			return errors.New("remote environment conflicts with execution environment none")
		}
		if !caps.RemoteEnvironment {
			return errors.New("engine does not support a remote execution environment")
		}
	}
	if req.DisableExecutionEnvironment && !caps.EnvironmentNone {
		return errors.New("engine does not support execution environment none")
	}
	return validateMCPHTTPBearer(req, caps)
}
