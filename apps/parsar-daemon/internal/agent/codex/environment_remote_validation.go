package codex

import (
	"errors"
	"net"
	"net/url"
	"os"
	"path"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func validateRemoteEnvironmentRequest(req proto.PromptRequestPayload) error {
	environment := req.RemoteEnvironment
	if environment == nil {
		return nil
	}
	if req.DisableExecutionEnvironment {
		return errors.New("codex: remote environment conflicts with environment none")
	}
	if !req.ReleaseOnCompletion || !req.StrictResume || strings.TrimSpace(req.AgentStateKey) == "" {
		return errors.New("codex: remote environment requires completion release, strict resume and a stable state key")
	}
	if req.WorkspaceAuthoring || len(req.Attachments) != 0 {
		return errors.New("codex: remote authoring and attachments are not supported")
	}
	for _, key := range []string{"skills", "mcp_servers", "plugin_dirs"} {
		if hasLocalEnvironmentOption(req.AgentOptions[key]) {
			return errors.New("codex: remote environment does not support local managed skills, MCP or plugins")
		}
	}
	if environment.ID == "" || environment.ID == "." || environment.ID == ".." || url.PathEscape(environment.ID) != environment.ID {
		return errors.New("codex: remote environment requires a valid identity")
	}
	if !path.IsAbs(environment.WorkspaceDirectory) || strings.ContainsAny(environment.WorkspaceDirectory, "\x00\r\n\\") {
		return errors.New("codex: remote workspace must be an absolute POSIX path")
	}
	if strings.TrimSpace(environment.ConnectionToken) == "" || strings.ContainsAny(environment.ConnectionToken, "\x00\r\n") {
		return errors.New("codex: remote environment requires a valid connection credential")
	}
	u, err := url.Parse(environment.ConnectionURL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("codex: remote connection URL must be an absolute origin without credentials")
	}
	switch u.Scheme {
	case "https":
	case "http":
		ip := net.ParseIP(u.Hostname())
		if !strings.EqualFold(u.Hostname(), "localhost") && (ip == nil || !ip.IsLoopback()) {
			return errors.New("codex: remote HTTP connections require loopback")
		}
	default:
		return errors.New("codex: remote connection requires HTTPS or loopback HTTP")
	}
	for _, entry := range os.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		if reservedRemoteEnvironmentVariable(key) && value != "" {
			return errors.New("codex: remote environment conflicts with native transport process configuration")
		}
	}
	env, err := buildSessionEnv(req.AgentOptions)
	if err != nil {
		return err
	}
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if reservedRemoteEnvironmentVariable(key) {
			return errors.New("codex: remote environment conflicts with native transport agent options")
		}
	}
	return nil
}

func reservedRemoteEnvironmentVariable(key string) bool {
	return strings.HasPrefix(strings.ToUpper(key), "CODEX_EXEC_SERVER_")
}

func hasLocalEnvironmentOption(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case []any:
		return len(v) != 0
	case []string:
		return len(v) != 0
	case map[string]any:
		return len(v) != 0
	case map[string]string:
		return len(v) != 0
	default:
		return true
	}
}
