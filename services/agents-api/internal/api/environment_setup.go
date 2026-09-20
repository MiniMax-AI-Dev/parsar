package api

import (
	"bytes"
	"encoding/json"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func decodeEnvironmentSetup(fields map[string]json.RawMessage) (store.EnvironmentSetup, error) {
	var result store.EnvironmentSetup
	if value, ok := fields["env"]; ok {
		var entries map[string]*string
		if json.Unmarshal(value, &entries) != nil {
			return result, store.ErrInvalidInput
		}
		result.Env = make(map[string]string, len(entries))
		for name, entry := range entries {
			if entry == nil {
				return result, store.ErrInvalidInput
			}
			result.Env[name] = *entry
		}
	}
	if value, ok := fields["setup_commands"]; ok {
		var commands []json.RawMessage
		if json.Unmarshal(value, &commands) != nil {
			return result, store.ErrInvalidInput
		}
		for _, command := range commands {
			var input struct {
				Command *string `json:"command"`
				CWD     *string `json:"cwd"`
			}
			if decodeInputObject(command, &input, "command", "cwd") != nil || input.Command == nil {
				return result, store.ErrInvalidInput
			}
			step := store.SetupCommand{Command: *input.Command}
			if input.CWD != nil {
				if *input.CWD == "" {
					return result, store.ErrInvalidInput
				}
				step.CWD = *input.CWD
			}
			result.Commands = append(result.Commands, step)
		}
	}
	if value, ok := fields["packages"]; ok && !bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		var input struct {
			NPM    []*string `json:"npm"`
			Python []*string `json:"python"`
			System []*string `json:"system"`
		}
		if decodeInputObject(value, &input, "npm", "python", "system") != nil {
			return result, store.ErrInvalidInput
		}
		for name, entries := range map[string][]*string{"npm": input.NPM, "python": input.Python, "system": input.System} {
			var target *[]string
			switch name {
			case "npm":
				target = &result.Packages.NPM
			case "python":
				target = &result.Packages.Python
			case "system":
				target = &result.Packages.System
			}
			for _, entry := range entries {
				if entry == nil {
					return result, store.ErrInvalidInput
				}
				*target = append(*target, *entry)
			}
		}
	}
	var err error
	result.Skills, err = decodeInlineSkills(fields["skills"])
	if err != nil {
		return result, err
	}
	return result, result.Validate()
}

func packageMetadata(packages *v1.EnvironmentPackages) v1.EnvironmentPackages {
	value := store.EnvironmentSetup{}
	if packages != nil {
		value.Packages = *packages
	}
	return value.PackageMetadata()
}
