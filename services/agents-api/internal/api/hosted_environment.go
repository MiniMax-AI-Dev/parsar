package api

import (
	"bytes"
	"encoding/json"
	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

// decodeHostedEnvironment keeps unsupported installations explicit, while
// accepting the protocol's omitted/null/empty defaults for the basic profile.
func decodeHostedEnvironment(raw json.RawMessage) (*v1.Environment, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil, store.ErrInvalidInput
	}
	env := &v1.Environment{Type: "openai_hosted", Network: &v1.EnvironmentNetworkInput{Access: "enabled"}}
	for name, value := range fields {
		switch name {
		case "type":
		case "network":
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				continue
			}
			var network v1.EnvironmentNetworkInput
			if decodeInputObject(value, &network, "access", "allowed_domains") != nil || (network.Access != "enabled" && network.Access != "disabled") || len(network.AllowedDomains) != 0 {
				return nil, store.ErrInvalidInput
			}
			env.Network = &network
		case "files":
			files, err := decodeInitialFiles(value)
			if err != nil {
				return nil, err
			}
			env.Files = initialFileResponse(files)
		case "capability_directories", "plugins", "skills", "setup_commands":
			var list []json.RawMessage
			if json.Unmarshal(value, &list) != nil || len(list) != 0 {
				return nil, store.ErrInvalidInput
			}
		case "env":
			var entries map[string]string
			if json.Unmarshal(value, &entries) != nil || len(entries) != 0 {
				return nil, store.ErrInvalidInput
			}
		case "packages":
			if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				continue
			}
			var packages v1.EnvironmentPackages
			if decodeInputObject(value, &packages, "npm", "python", "system") != nil || len(packages.NPM)+len(packages.Python)+len(packages.System) != 0 {
				return nil, store.ErrInvalidInput
			}
		default:
			return nil, store.ErrInvalidInput
		}
	}
	return env, nil
}

// Hosted metadata describes API-managed initial installations, not workspace inventory.
func hostedSessionEnvironment(environment store.Environment) (v1.SessionEnvironment, error) {
	cfg, err := storedEnvironment(environment.Configuration)
	if err != nil || cfg.Type != "openai_hosted" {
		return v1.SessionEnvironment{}, store.ErrInvalidInput
	}
	empty := []json.RawMessage{}
	files := cfg.Files
	if files == nil {
		files = []json.RawMessage{}
	}
	directories := []string{}
	return v1.SessionEnvironment{ID: environment.ID, Type: cfg.Type, CapabilityDirectories: &directories,
		Network:  &v1.EnvironmentNetwork{Access: cfg.Network.Access, AllowedDomains: []string{}},
		Packages: &v1.EnvironmentPackages{NPM: []string{}, Python: []string{}, System: []string{}}, Files: &files, Plugins: &empty, Skills: &empty}, nil
}

// WithHostedEnvironments enables admission only for an operator-composed,
// qualified managed Runtime deployment. Native capability flags cannot enable it.
func WithHostedEnvironments() Option {
	return func(h *Handler) { h.hostedEnvironments = true }
}

func storedEnvironment(raw json.RawMessage) (*v1.Environment, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil, store.ErrInvalidInput
	}
	var kind string
	if json.Unmarshal(fields["type"], &kind) != nil {
		return nil, store.ErrInvalidInput
	}
	if kind != "openai_hosted" {
		return decodeSessionEnvironment(raw)
	}
	var files []json.RawMessage
	if value, exists := fields["files"]; exists {
		if json.Unmarshal(value, &files) != nil || len(files) > 50 {
			return nil, store.ErrInvalidInput
		}
		for _, entry := range files {
			var metadata store.InitialFileMetadata
			if decodeInputObject(entry, &metadata, "id", "type", "path", "file_id", "size_bytes") != nil || metadata.ID == "" || metadata.SizeBytes == nil || *metadata.SizeBytes < 0 || *metadata.SizeBytes > store.MaxInitialFileBytes {
				return nil, store.ErrInvalidInput
			}
			if metadata.Type != "inline" && metadata.Type != "file_id" {
				return nil, store.ErrInvalidInput
			}
		}
	}
	delete(fields, "files")
	base, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	cfg, err := decodeHostedEnvironment(base)
	if err != nil {
		return nil, err
	}
	cfg.Files = files
	return cfg, nil
}
