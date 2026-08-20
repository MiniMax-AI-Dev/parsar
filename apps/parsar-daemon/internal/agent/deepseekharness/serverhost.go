package deepseekharness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/enginehost"
)

// serverSupervisor is process-wide because the resident servers are: a
// state key's engine has to be shared by every prompt of that key, and
// agent.Factory is a plain function with nowhere to hang per-daemon
// state. Shutdown is wired to daemon teardown by the dispatch layer.
var serverSupervisor = enginehost.NewSupervisor(nil)

// ShutdownServers stops every resident dsh server. Called on daemon
// teardown so an engine does not outlive the process that started it.
func ShutdownServers() { serverSupervisor.Shutdown() }

// readyProbeTimeout bounds one readiness attempt. A cold dsh boot
// compiles its plugin tree, so the overall gate is generous while each
// individual probe stays short.
const readyProbeTimeout = 10 * time.Second

// serverLaunch is everything one resident server's identity and launch
// depend on.
type serverLaunch struct {
	Home        string
	WorkDir     string
	Binary      string
	Provider    providerConfig
	HasProvider bool
	Model       string
	ProviderID  string
	Env         []string
	StateKey    string
}

// spec turns a launch into an enginehost.ServerSpec.
func (l serverLaunch) spec() enginehost.ServerSpec {
	return enginehost.ServerSpec{
		Key:    l.key(),
		Binary: l.Binary,
		Dir:    l.WorkDir,
		Args: func(int) []string {
			// The port reaches dsh through the generated profile, not the
			// command line: dsh has no port flag, the webserver row owns
			// it. Prepare writes that row before this argv is used.
			return []string{"--profile", serverProfileName}
		},
		Env: func(int) []string { return l.Env },
		Prepare: func(_ context.Context, port int) error {
			if err := os.MkdirAll(l.Home, 0o700); err != nil {
				return fmt.Errorf("deepseekharness: mkdir dsh home %s: %w", l.Home, err)
			}
			return writeServerProfile(serverProfileSpec{
				Home:        l.Home,
				Port:        port,
				Provider:    l.Provider,
				HasProvider: l.HasProvider,
				Model:       l.Model,
				ProviderID:  l.ProviderID,
			})
		},
		Ready:       probeReady,
		IdleTimeout: serverIdleTimeout(),
		Logger:      nil,
	}
}

// key is the reuse identity. It is the state key plus a fingerprint of
// everything baked into the generated profile at launch.
//
// The fingerprint matters: the resident server reads its model route,
// credentials env and workspace once, at boot. If a later prompt of the
// same conversation selects a different model or a rotated key, reusing
// the running server would silently run the turn on the old route. Making
// those inputs part of the key means such a prompt gets its own server
// instead, and the stale one is reclaimed when it goes idle.
func (l serverLaunch) key() string {
	h := sha256.New()
	for _, part := range []string{
		l.Home, l.WorkDir, l.Binary,
		l.Provider.BaseURL, l.Provider.API, l.Provider.APIKeyEnv, l.Provider.Model,
		l.Model, l.ProviderID,
	} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	for _, k := range sortedKeys(l.Provider.Headers) {
		h.Write([]byte(k + "=" + l.Provider.Headers[k]))
		h.Write([]byte{0})
	}
	// The env is hashed, never recorded: it carries the API key value.
	env := append([]string{}, l.Env...)
	sort.Strings(env)
	for _, e := range env {
		h.Write([]byte(e))
		h.Write([]byte{0})
	}
	name := strings.TrimSpace(l.StateKey)
	if name == "" {
		name = "unkeyed"
	}
	return name + ":" + hex.EncodeToString(h.Sum(nil)[:8])
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// probeReady is the ServerSpec readiness gate. session.list is used
// rather than a TCP connect because a bound port proves only that the
// web server row came up: the gateway, its carrier and the session store
// are separate rows, and a profile missing any of them answers 404 on a
// listening socket.
func probeReady(ctx context.Context, baseURL string) error {
	client := newAPIClient(enginehost.NewClient(baseURL, readyProbeTimeout))
	_, err := client.ListSessions(ctx)
	return err
}

// serverIdleTimeout is how long a conversation's engine stays warm after
// its last prompt. Overridable so an operator can trade memory for
// first-turn latency without a rebuild.
func serverIdleTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv("PARSAR_DSH_SERVER_IDLE"))
	if raw == "" {
		return enginehost.DefaultIdleTimeout
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d == 0 {
		return enginehost.DefaultIdleTimeout
	}
	return d
}
