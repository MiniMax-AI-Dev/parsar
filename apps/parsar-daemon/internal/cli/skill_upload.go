package cli

import (
	"context"
	"maps"
	"os"
	"path/filepath"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func withSkillUploadServer(factory agent.Factory, serverURL string) agent.Factory {
	return func(ctx context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope) (agent.Session, error) {
		env, _ := req.AgentOptions["env"].(map[string]any)
		if token, _ := env["PARSAR_CAPABILITY_UPLOAD_TOKEN"].(string); token != "" {
			env = maps.Clone(env)
			env["PARSAR_SERVER_URL"] = serverURL
			if executable, err := os.Executable(); err == nil {
				addCompanionCLIPath(env, filepath.Dir(executable))
			}
			req.AgentOptions = maps.Clone(req.AgentOptions)
			req.AgentOptions["env"] = env
		}
		return factory(ctx, req, out)
	}
}

func addCompanionCLIPath(env map[string]any, dir string) {
	info, err := os.Stat(filepath.Join(dir, "parsar"))
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return
	}
	path, ok := env["PATH"].(string)
	if !ok {
		path = os.Getenv("PATH")
	}
	env["PATH"] = dir + string(os.PathListSeparator) + path
}
