package main

import (
	"net/url"
	"os"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	agentdaemonbinding "github.com/MiniMax-AI-Dev/parsar/server/internal/agentdaemon/binding"
	agentdaemongateway "github.com/MiniMax-AI-Dev/parsar/server/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/config"
	connagentdaemon "github.com/MiniMax-AI-Dev/parsar/server/internal/connector/agentdaemon"
	dockersandbox "github.com/MiniMax-AI-Dev/parsar/server/internal/sandbox/docker"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// dockerDialBackURL rewrites a loopback ServerURL so a sandbox container
// can reach the host-run server. A daemon inside the container cannot dial
// 127.0.0.1/localhost/::1 (that's the container itself); Docker exposes the
// host as host.docker.internal. Returns the rewritten URL and whether the
// host-gateway mapping is needed (non-loopback URLs pass through untouched).
func dockerDialBackURL(serverURL string) (string, bool) {
	u, err := url.Parse(strings.TrimSpace(serverURL))
	if err != nil || u.Host == "" {
		return serverURL, false
	}
	host := u.Hostname()
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return serverURL, false
	}
	newHost := "host.docker.internal"
	if port := u.Port(); port != "" {
		newHost += ":" + port
	}
	u.Host = newHost
	return u.String(), true
}

// buildDockerAgentDaemonSandboxProvider wires a local-docker-backed
// SandboxProvider for the agent_daemon connector. Returns nil when the
// docker backend is not requested (caller falls back to the e2b builder,
// then NoopSandboxProvider).
//
// Env vars:
//   - AGENT_DAEMON_SANDBOX_BACKEND — must equal "docker" to select this.
//   - AGENT_DAEMON_SANDBOX_DOCKER_IMAGE — local image tag to run.
//   - AGENT_DAEMON_SANDBOX_DOCKER_NETWORK — optional docker network to join
//     (use the compose network when the server runs as a compose service).
func buildDockerAgentDaemonSandboxProvider(
	env func(string) string,
	cfg config.Config,
	dbStore *store.Store,
	registry *agentdaemongateway.Registry,
	binder agentdaemonbinding.Binder,
	selfPodID string,
) connagentdaemon.SandboxProvider {
	if env == nil {
		env = os.Getenv
	}
	if strings.TrimSpace(env("AGENT_DAEMON_SANDBOX_BACKEND")) != "docker" {
		return nil
	}
	image := strings.TrimSpace(env("AGENT_DAEMON_SANDBOX_DOCKER_IMAGE"))
	if image == "" {
		log.Bg().Warn("agent_daemon docker sandbox disabled: AGENT_DAEMON_SANDBOX_BACKEND=docker but AGENT_DAEMON_SANDBOX_DOCKER_IMAGE is empty")
		return nil
	}

	publicURL := strings.TrimSpace(cfg.Server.PublicURL)
	if publicURL == "" {
		publicURL = "http://127.0.0.1:18080"
	}
	network := strings.TrimSpace(env("AGENT_DAEMON_SANDBOX_DOCKER_NETWORK"))
	serverURL, hostGateway := dockerDialBackURL(publicURL)
	// When joined to a user-defined docker network the server is reachable
	// by service name, so the loopback rewrite/host-gateway is unnecessary.
	if network != "" {
		serverURL = publicURL
		hostGateway = false
	}

	client := &dockersandbox.Client{
		Image:       image,
		Network:     network,
		HostGateway: hostGateway,
	}
	provider, err := connagentdaemon.NewE2BSandboxProvider(connagentdaemon.E2BProviderConfig{
		Client:       client,
		Store:        dbStore,
		Registry:     registry,
		Binder:       binder,
		Bindings:     dbStore,
		Template:     image,
		Templates:    map[string]string{"standard": image},
		DefaultSize:  "standard",
		ServerURL:    serverURL,
		OwnerChecker: dbStore,
		SelfPodID:    selfPodID,
		Log:          log.Bg(),
	})
	if err != nil {
		log.Bg().Warn("agent_daemon docker sandbox provider init failed; docker backend disabled", "error", err)
		return nil
	}
	log.Bg().Info("agent_daemon docker sandbox provider wired",
		"image", image,
		"network", network,
		"server_url", serverURL,
		"host_gateway", hostGateway)
	return provider
}
