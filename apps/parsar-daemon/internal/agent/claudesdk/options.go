package claudesdk

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/paths"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type Config struct {
	Node       string
	Entrypoint string
	StateDir   string
	Env        []string
}

type startRequest struct {
	Type         string `json:"type"`
	Prompt       string `json:"prompt"`
	Model        string `json:"model"`
	SystemPrompt string `json:"system_prompt"`
	Cwd          string `json:"cwd"`
	Resume       string `json:"resume,omitempty"`
}

func prepare(config Config, req proto.PromptRequestPayload) (startRequest, []string, error) {
	start := startRequest{Type: "start", Prompt: req.Prompt, Resume: req.AgentSessionID}
	fail := func(reason string) (startRequest, []string, error) {
		return startRequest{}, nil, fmt.Errorf("claudesdk: %s", reason)
	}
	if req.RunID == "" || strings.TrimSpace(req.Prompt) == "" {
		return fail("run id and prompt are required")
	}
	if len(req.Attachments) > 0 || len(req.FunctionTools) > 0 || req.WorkspaceAuthoring || req.ObserveMessages || req.ObserveTools || req.ObserveToolObservations || req.DisableExecutionEnvironment || req.DisableSubagents {
		return fail("requested capability is not available in the text adapter")
	}
	for name, raw := range req.AgentOptions {
		value, ok := raw.(string)
		if !ok {
			return fail("model and system_prompt options must be strings")
		}
		switch name {
		case "model":
			start.Model = value
		case "system_prompt":
			start.SystemPrompt = value
		default:
			return fail("unsupported option: " + name)
		}
	}
	if strings.TrimSpace(start.Model) == "" {
		return fail("model is required")
	}
	if !filepath.IsAbs(config.Entrypoint) {
		return fail("SDK entrypoint must be absolute")
	}
	root, err := paths.Root()
	if err != nil {
		return startRequest{}, nil, err
	}
	relative, err := filepath.Rel(root, config.StateDir)
	if err != nil || !filepath.IsAbs(root) || !filepath.IsAbs(config.StateDir) || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fail("SDK state must be in a managed runtime subdirectory")
	}
	start.Cwd = req.WorkDir
	if start.Cwd == "" {
		start.Cwd = filepath.Join(config.StateDir, "work")
	}
	if strings.HasPrefix(start.Cwd, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return startRequest{}, nil, err
		}
		start.Cwd = filepath.Join(homeDir, strings.TrimPrefix(start.Cwd, "~/"))
	}
	if !filepath.IsAbs(start.Cwd) {
		return fail("work_dir must be absolute or start with ~/")
	}
	for _, dir := range []string{config.StateDir, filepath.Join(config.StateDir, "tmp"), start.Cwd} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return startRequest{}, nil, err
		}
	}
	env := append(append([]string{}, os.Environ()...), config.Env...)
	env = append(env, "CLAUDE_CONFIG_DIR="+config.StateDir, "TMPDIR="+filepath.Join(config.StateDir, "tmp"), "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1")
	return start, env, nil
}
