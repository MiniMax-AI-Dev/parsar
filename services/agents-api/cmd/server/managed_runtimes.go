package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	sandboxdocker "github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/docker"
	sandboxe2b "github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/e2b"
	"github.com/moby/moby/client"
)

type managedRuntimeConfig struct {
	CoreURL         string                         `json:"core_url"`
	DefaultProvider string                         `json:"default_provider"`
	Docker          map[string]managedDockerConfig `json:"docker"`
	E2B             map[string]managedE2BConfig    `json:"e2b"`
}

type managedE2BConfig struct {
	APIKeyFile   string `json:"api_key_file"`
	Template     string `json:"template"`
	LeaseSeconds int    `json:"lease_seconds"`
}

type managedDockerConfig struct {
	Host          string   `json:"host"`
	Image         string   `json:"image"`
	Network       string   `json:"network"`
	SeccompFile   string   `json:"seccomp_file"`
	ExtraHosts    []string `json:"extra_hosts"`
	NestedSandbox bool     `json:"nested_sandbox"`
}

// Each provider key pins an explicit backend, independently of ambient
// DOCKER_HOST. Retained entries keep their cleanup backend when the default changes.
func managedRuntimes() (*execution.RuntimeProviders, func(), error) {
	file := os.Getenv("AGENTS_API_MANAGED_RUNTIMES_FILE")
	closeAll := func() {}
	if file == "" {
		return nil, closeAll, nil
	}
	if os.Getenv("AGENTS_API_DAEMON_WS_URL") == "" {
		return nil, closeAll, errors.New("managed Runtime configuration requires AGENTS_API_DAEMON_WS_URL")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, closeAll, errors.New("cannot read AGENTS_API_MANAGED_RUNTIMES_FILE")
	}
	var config managedRuntimeConfig
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&config) != nil || decoder.Decode(new(any)) != io.EOF || len(config.Docker)+len(config.E2B) == 0 {
		return nil, closeAll, errors.New("invalid managed Runtime configuration")
	}
	if config.DefaultProvider != "" {
		_, dockerOK := config.Docker[config.DefaultProvider]
		_, e2bOK := config.E2B[config.DefaultProvider]
		if !dockerOK && !e2bOK {
			return nil, closeAll, errors.New("managed default provider is not configured")
		}
	}
	var clients []*client.Client
	closeAll = func() {
		for _, c := range clients {
			_ = c.Close()
		}
	}
	result := &execution.RuntimeProviders{CoreURL: config.CoreURL, DefaultProvider: config.DefaultProvider, Providers: map[string]sandbox.Provider{}}
	for key, entry := range config.E2B {
		if _, duplicate := config.Docker[key]; duplicate {
			return nil, closeAll, errors.New("managed provider keys must be unique")
		}
		keyBytes, err := os.ReadFile(entry.APIKeyFile)
		if err != nil {
			return nil, closeAll, errors.New("cannot read managed E2B API key file")
		}
		provider, err := sandboxe2b.New(sandboxe2b.Config{InstallationID: key, APIKey: strings.TrimSpace(string(keyBytes)), Template: entry.Template, LeaseSeconds: entry.LeaseSeconds})
		if err != nil {
			return nil, closeAll, errors.New("invalid managed E2B provider configuration")
		}
		result.Providers[key] = provider
	}
	for key, entry := range config.Docker {
		// V1 qualifies a local Docker daemon. Remote executor/provider transports are
		// separate work; do not silently inherit a different backend from the shell.
		if !strings.HasPrefix(entry.Host, "unix:///") || len(entry.Host) <= 8 {
			closeAll()
			return nil, func() {}, errors.New("managed Docker host must be an explicit unix socket")
		}
		seccomp, err := os.ReadFile(entry.SeccompFile)
		if err != nil || !json.Valid(seccomp) {
			closeAll()
			return nil, func() {}, errors.New("cannot read managed Docker seccomp JSON")
		}
		c, err := client.New(client.WithHost(entry.Host))
		if err != nil {
			closeAll()
			return nil, func() {}, errors.New("invalid managed Docker endpoint")
		}
		clients = append(clients, c)
		provider, err := sandboxdocker.New(c, sandboxdocker.Config{InstallationID: key, Image: entry.Image, Network: entry.Network, Seccomp: string(seccomp), ExtraHosts: entry.ExtraHosts, NestedSandbox: entry.NestedSandbox})
		if err != nil {
			closeAll()
			return nil, func() {}, errors.New("invalid managed Docker provider configuration")
		}
		result.Providers[key] = provider
	}
	return result, closeAll, nil
}
