package store

import (
	"fmt"
	"strings"
)

func mergeAgentProfileOwnedConfig(current, requested map[string]any) (map[string]any, error) {
	config := current
	profileConfig := nonNilMap(requested)
	previousAgentKind := stringFromMap(config, "agent_kind")

	if raw, ok := profileConfig["profile"]; ok {
		profile, ok := raw.(map[string]any)
		if !ok || len(profile) == 0 {
			delete(config, "profile")
		} else {
			config["profile"] = cloneAnyMap(profile)
		}
	}
	for _, key := range []string{"agent_kind", "daemon_mode", "sandbox_size", "device_id"} {
		raw, ok := profileConfig[key]
		if !ok {
			continue
		}
		value, _ := raw.(string)
		if value = strings.TrimSpace(value); value == "" {
			delete(config, key)
		} else {
			config[key] = value
		}
	}

	var profileWorkdir any
	var profileWorkdirSet bool
	for _, key := range []string{"work_dir", "workdir", "working_directory"} {
		if value, ok := profileConfig[key]; ok {
			profileWorkdir = value
			profileWorkdirSet = true
			break
		}
	}
	if profileWorkdirSet {
		delete(config, "work_dir")
		delete(config, "workdir")
		delete(config, "working_directory")
		if workdir, ok := profileWorkdir.(string); ok {
			if workdir = strings.TrimSpace(workdir); workdir != "" {
				config["work_dir"] = workdir
			}
		}
	}

	if raw, modeSet := profileConfig["mode"]; modeSet {
		agentKind := stringFromMap(config, "agent_kind")
		mode, keep, err := normalizeAgentMode(agentKind, raw)
		if err != nil {
			return nil, err
		}
		if !keep {
			delete(config, "mode")
		} else {
			config["mode"] = mode
		}
	} else if previousAgentKind == "codex" && stringFromMap(config, "agent_kind") != "codex" {
		// A Codex plan/default value is not a portable permission mode for
		// another engine. Clear it when the engine changes even if an older
		// client omitted the explicit null.
		delete(config, "mode")
	}

	if _, daemonModeSet := profileConfig["daemon_mode"]; daemonModeSet {
		switch stringFromMap(config, "daemon_mode") {
		case "sandbox":
			delete(config, "device_id")
		case "local":
			delete(config, "sandbox_size")
		}
	}
	return config, nil
}

func foldAgentDaemonConfig(config, input map[string]any) error {
	for _, key := range []string{"device_id", "daemon_mode", "agent_kind", "work_dir"} {
		if value, ok := input[key].(string); ok {
			if value = strings.TrimSpace(value); value != "" {
				config[key] = value
			}
		}
	}

	rawMode, modeSet := input["mode"]
	if !modeSet {
		return nil
	}
	mode, keep, err := normalizeAgentMode(stringFromMap(config, "agent_kind"), rawMode)
	if err != nil {
		return err
	}
	if !keep {
		return nil
	}
	config["mode"] = mode
	return nil
}

func normalizeAgentMode(agentKind string, raw any) (mode string, keep bool, err error) {
	if raw == nil {
		return "", false, nil
	}
	value, ok := raw.(string)
	if !ok {
		return "", false, fmt.Errorf("%w: agent mode must be a string", ErrInvalidInput)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false, nil
	}
	if agentKind == "codex" && value != "default" && value != "plan" {
		return "", false, fmt.Errorf("%w: Codex collaboration mode must be default or plan", ErrInvalidInput)
	}
	return value, true, nil
}
