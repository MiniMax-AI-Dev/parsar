package dispatch

import (
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func validateExecutionEnvironment(req proto.PromptRequestPayload, caps proto.AgentKindCapabilities) error {
	if req.LocalEnvironment != nil && (req.RemoteEnvironment != nil || req.DisableExecutionEnvironment || !caps.LocalEnvironment) {
		return errors.New("engine does not support this local Environment configuration")
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
	return validateMCPHTTP(req, caps)
}
