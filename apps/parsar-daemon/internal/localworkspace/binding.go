package localworkspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

// Binding freezes operator-owned identity and paths for one Runtime lifetime.
type Binding struct {
	environment   string
	networkAccess string
	stateKey      string
	workspace     string
	helper        string
	exportHelper  string
	writer        *fileWriter
}

func New(environment, session, workspace, helper string) (*Binding, error) {
	for _, id := range []string{environment, session} {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed == uuid.Nil || parsed.String() != id {
			return nil, errors.New("local workspace requires canonical resource identities")
		}
	}
	for _, name := range []string{workspace, helper} {
		if !filepath.IsAbs(name) || filepath.Clean(name) != name || name == "/" || strings.ContainsAny(name, "\x00\r\n\\") {
			return nil, errors.New("local workspace requires clean absolute deployment paths")
		}
	}
	root, err := os.Lstat(workspace)
	if err != nil || !root.IsDir() || root.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("local workspace root must be an existing directory")
	}
	program, err := os.Stat(helper)
	if err != nil || !program.Mode().IsRegular() || program.Mode().Perm()&0111 == 0 || strings.HasPrefix(helper, workspace+string(filepath.Separator)) {
		return nil, errors.New("local workspace helper must be executable outside the workspace")
	}
	return &Binding{environment: environment, stateKey: "agents-api-" + session, workspace: workspace, helper: helper}, nil
}

func Load() (*Binding, error) {
	values := []string{os.Getenv("PARSAR_RUNTIME_ENVIRONMENT_ID"), os.Getenv("PARSAR_RUNTIME_SESSION_ID"), os.Getenv("PARSAR_RUNTIME_WORKSPACE"), os.Getenv("PARSAR_RUNTIME_DIRECTORY_HELPER")}
	network := os.Getenv("PARSAR_RUNTIME_NETWORK_ACCESS")
	if network != "" && network != "enabled" && network != "disabled" {
		return nil, errors.New("unsupported local Runtime network policy")
	}
	writeHelper, staging := os.Getenv("PARSAR_RUNTIME_WRITE_HELPER"), os.Getenv("PARSAR_RUNTIME_STAGING")
	exportHelper := os.Getenv("PARSAR_RUNTIME_EXPORT_HELPER")
	if strings.Join(values, "") == "" && writeHelper == "" && staging == "" && network == "" && exportHelper == "" {
		return nil, nil
	}
	b, err := New(values[0], values[1], values[2], values[3])
	if err != nil {
		return nil, err
	}
	b.networkAccess = network
	if exportHelper != "" {
		// Reuse the startup executable/root checks; this grants no caller authority.
		if _, err := New(values[0], values[1], values[2], exportHelper); err != nil {
			return nil, err
		}
		resolved, err := filepath.EvalSymlinks(exportHelper)
		if err != nil || resolved != exportHelper {
			return nil, errors.New("workspace exporter must be a canonical executable")
		}
		b.exportHelper = exportHelper
	}
	if writeHelper != "" || staging != "" {
		if err := b.bindWriter(writeHelper, staging); err != nil {
			return nil, err
		}
	}
	return b, nil
}

// Configure validates the reference before supplying the immutable local cwd.
func (b *Binding) Configure(r proto.PromptRequestPayload) (proto.PromptRequestPayload, error) {
	if b == nil && r.LocalEnvironment == nil {
		return r, nil
	}
	if b == nil || r.LocalEnvironment == nil || r.LocalEnvironment.ID != b.environment || r.AgentStateKey != b.stateKey ||
		r.RemoteEnvironment != nil || r.DisableExecutionEnvironment || r.WorkDir != "" ||
		r.ConversationID != "" || r.WorkspaceAuthoring || len(r.Attachments) != 0 || !r.StrictResume || !r.ReleaseOnCompletion {
		return r, errors.New("request does not match the dedicated local Environment")
	}
	if (!r.WorkspaceReadOnly || r.LocalEnvironment.NetworkAccess != "") && r.LocalEnvironment.NetworkAccess != b.networkAccess {
		return r, errors.New("request does not match the local Runtime network policy")
	}
	if !r.WorkspaceReadOnly {
		if r.LocalEnvironment.ToolEnvironment {
			if err := VerifyToolEnvironment(); err != nil {
				return r, err
			}
		}
		r.WorkDir = b.workspace
	}
	return r, nil
}

// NetworkAccess is deployment-owned; read-only workspace controls need no network.
func (b *Binding) NetworkAccess() string { return b.networkAccess }
