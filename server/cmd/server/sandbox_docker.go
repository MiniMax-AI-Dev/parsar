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

// resolveAgentDaemonPublicWSURL returns the ws:// URL the daemon dials after
// bootstrap. It starts from the scheme-swapped PublicURL
// (buildAgentDaemonWSURL) and, when the local-docker sandbox backend is active
// without a user-defined docker network, applies the same loopback →
// host.docker.internal rewrite used for the bootstrap ServerURL. Without it a
// container daemon dials 127.0.0.1 — itself — never reaching the host server,
// so the run stays unassigned. A configured network makes the server reachable
// by service name, so the loopback URL is left intact (mirrors the gating in
// buildDockerAgentDaemonSandboxProvider).
func resolveAgentDaemonPublicWSURL(env func(string) string, cfg config.Config) string {
	if env == nil {
		env = os.Getenv
	}
	base := buildAgentDaemonWSURL(cfg)
	if strings.TrimSpace(env("AGENT_DAEMON_SANDBOX_BACKEND")) != "docker" {
		return base
	}
	if strings.TrimSpace(env("AGENT_DAEMON_SANDBOX_DOCKER_NETWORK")) != "" {
		return base
	}
	rewritten, _ := dockerDialBackURL(base)
	return rewritten
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
//   - AGENT_DAEMON_SANDBOX_DOCKER_MEMORY / _CPUS — optional `docker run`
//     resource caps; unset = built-in default (4g / 2 CPU). Set to
//     0/unlimited/none to remove the cap.
//   - AGENT_DAEMON_SANDBOX_DOCKER_PIDS_LIMIT — optional pids cap; unset = no
//     cap (docker default).
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

	client := dockerClientFromEnv(env, image, network, hostGateway)
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
		"host_gateway", hostGateway,
		"memory", client.Memory,
		"cpus", client.CPUs,
		"pids_limit", client.PidsLimit)
	return provider
}

// Built-in sandbox caps used when the operator sets no override: a
// conservative default stops one runaway sandbox starving the host (raise it
// with the env var, or disable it with 0/unlimited). PidsLimit has no default
// — a low pids cap breaks parallel builds (`make -j`, `go test ./...`).
const (
	defaultDockerMemory = "4g"
	defaultDockerCPUs   = "2"
)

// dockerClientFromEnv builds the docker sandbox client, resolving the
// AGENT_DAEMON_SANDBOX_DOCKER_{MEMORY,CPUS,PIDS_LIMIT} caps: memory and cpus
// fall back to the built-in default when unset, pids stays off; see
// resolveDockerLimit for the 0/unlimited escape hatch.
func dockerClientFromEnv(env func(string) string, image, network string, hostGateway bool) *dockersandbox.Client {
	return &dockersandbox.Client{
		Image:       image,
		Network:     network,
		HostGateway: hostGateway,
		Memory:      resolveDockerLimit(env("AGENT_DAEMON_SANDBOX_DOCKER_MEMORY"), defaultDockerMemory),
		CPUs:        resolveDockerLimit(env("AGENT_DAEMON_SANDBOX_DOCKER_CPUS"), defaultDockerCPUs),
		PidsLimit:   resolveDockerLimit(env("AGENT_DAEMON_SANDBOX_DOCKER_PIDS_LIMIT"), ""),
	}
}

// resolveDockerLimit resolves one resource cap: empty env → built-in default;
// "0"/"unlimited"/"none" (case-insensitive) → "" so Create omits the flag
// (docker's unbounded default); otherwise the literal value, passed through
// unvalidated (a bad value fails `docker run`, which Create surfaces).
func resolveDockerLimit(raw, def string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def
	}
	switch strings.ToLower(raw) {
	case "0", "unlimited", "none":
		return ""
	}
	return raw
}
