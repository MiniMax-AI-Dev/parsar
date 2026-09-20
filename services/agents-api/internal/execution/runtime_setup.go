package execution

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentskill"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

// runtimeSetupOperation is the packaged initializer's confidential stdin contract.
// Public templates and native harness configuration never cross this boundary.
type runtimeSetupOperation struct {
	Skill    *store.InlineSkill `json:"-"`
	Name     string             `json:"name,omitempty"`
	Files    []agentskill.File  `json:"files,omitempty"`
	Version  int                `json:"version"`
	Action   string             `json:"action"`
	Network  string             `json:"network,omitempty"`
	Env      map[string]string  `json:"env"`
	Packages []string           `json:"packages,omitempty"`
	Command  string             `json:"command,omitempty"`
	CWD      string             `json:"cwd,omitempty"`
}

func setupOperations(setup store.EnvironmentSetup) []runtimeSetupOperation {
	if setup.Empty() {
		return nil
	}
	env := setup.Env
	if env == nil {
		env = map[string]string{}
	}
	result := []runtimeSetupOperation{{Version: 1, Action: "configure", Env: env}}
	for i := range setup.Skills {
		result = append(result, runtimeSetupOperation{Version: 1, Action: "skill", Skill: &setup.Skills[i]})
	}
	// The public network policy applies after setup completes. Provisioning uses
	// the isolated initializer's network; adapters enforce the runtime policy.
	const network = "enabled"
	if len(setup.Packages.NPM) > 0 {
		result = append(result, runtimeSetupOperation{Version: 1, Action: "npm", Network: network, Packages: setup.Packages.NPM})
	}
	if len(setup.Packages.Python) > 0 {
		result = append(result, runtimeSetupOperation{Version: 1, Action: "python", Network: network, Packages: setup.Packages.Python})
	}
	for _, command := range setup.Commands {
		cwd := command.CWD
		if cwd == "" {
			cwd = "/workspace"
		}
		result = append(result, runtimeSetupOperation{Version: 1, Action: "setup", Network: network, Command: command.Command, CWD: cwd})
	}
	return result
}

func runRuntimeSetup(ctx context.Context, provider sandbox.Provider, reference sandbox.Reference, operation runtimeSetupOperation) error {
	if provider == nil {
		return sandbox.ErrInvalid
	}
	if operation.Skill != nil {
		files, err := agentskill.Read(operation.Skill.Archive, operation.Skill.Metadata)
		if err != nil {
			return err
		}
		operation.Name, operation.Files = operation.Skill.Metadata.Name, files
	}
	input, err := json.Marshal(operation)
	if err != nil {
		return err
	}
	result, err := provider.RunCommand(ctx, reference, sandbox.Command{Directory: "/", Args: []string{"/usr/bin/python3", "-I", "-S", "/usr/local/bin/agents-api-runtime-initialize"}, Stdin: input})
	if err != nil {
		return err
	}
	var receipt struct {
		Version int    `json:"version"`
		Outcome string `json:"outcome"`
	}
	if result.ExitCode != 0 || result.Stderr != "" || json.Unmarshal([]byte(result.Stdout), &receipt) != nil || receipt.Version != 1 || receipt.Outcome != "completed" {
		return errors.New("environment initialization operation unconfirmed")
	}
	return nil
}
