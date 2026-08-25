// Package enginehost supervises long-lived local engine servers on behalf
// of agent adapters.
//
// Some agent CLIs expose their full capability surface (streaming events,
// approvals, cross-process session resume) only through a resident HTTP
// server rather than a one-shot invocation. Those adapters need the same
// four things, none of which is engine-specific:
//
//   - one server process per state key, shared by every prompt of that key
//     instead of relaunched per run,
//   - a loopback port that never leaves the machine,
//   - a readiness gate so the first prompt does not race the bind,
//   - idle reclamation so an abandoned conversation does not pin a process.
//
// Supervisor owns all four. An adapter contributes only a ServerSpec: how
// to lay out state, how to launch, and how to tell "listening" from
// "still booting". Nothing in this package names a concrete engine, and
// nothing here speaks an engine's wire protocol — adapters own their own
// request and event mapping (see Client for the transport helpers).
//
// Ownership boundary: a Supervisor hands out *Lease values, never raw
// processes. A lease keeps the instance alive; releasing the last lease
// starts the idle clock. Adapters must Release exactly once per Acquire,
// and must not retain a BaseURL past Release.
package enginehost

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Default timings. Each is deliberately generous: an engine server's
// first boot may compile a plugin tree or provision a state directory.
const (
	DefaultReadyTimeout = 90 * time.Second
	DefaultIdleTimeout  = 10 * time.Minute
	DefaultKillTimeout  = 5 * time.Second
)

// ServerSpec describes one engine server an adapter wants resident. The
// zero value is not usable: Key, Binary and Ready are required.
//
// Args and Env are functions of the assigned port because the supervisor
// picks the port, not the adapter — a spec that hardcoded a port could
// not be shared by two state keys on the same host.
type ServerSpec struct {
	// Key is the reuse identity. Two Acquire calls with equal keys share
	// one process. Adapters normally derive it from the daemon's
	// agent_state_key so a conversation keeps its resident engine (and,
	// with it, that engine's session store) across prompts.
	Key string

	// StateKey is the exclusive ownership identity for mutable engine state.
	// Specs with different Key values but the same StateKey never run at the
	// same time: the supervisor drains and stops the old variant before it
	// launches the replacement. Leave empty when Key already names both the
	// reusable process and its private state.
	StateKey string

	// Binary is the executable to launch, resolved through PATH.
	Binary string

	// Args returns the argv tail for the assigned port.
	Args func(port int) []string

	// Env returns the full environment for the process. A nil Env means
	// the process inherits the daemon's environment unchanged.
	Env func(port int) []string

	// Dir is the working directory. Engines that treat CWD as the
	// workspace root need this set; others may leave it empty.
	Dir string

	// Prepare runs once per launch, before the process starts. Adapters
	// use it to materialise a profile or config tree. A Prepare error
	// fails the Acquire without leaving a process behind.
	Prepare func(ctx context.Context, port int) error

	// Ready reports whether the server at baseURL is serving. It is
	// polled until it returns nil or ReadyTimeout elapses, so it must be
	// cheap and side-effect free. A nil error means "listening and
	// answering"; any error means "not yet".
	Ready func(ctx context.Context, baseURL string) error

	// ReadyTimeout bounds the readiness poll. Zero means
	// DefaultReadyTimeout.
	ReadyTimeout time.Duration

	// IdleTimeout is how long an instance with no live lease is kept
	// warm. Zero means DefaultIdleTimeout. Negative means "stop as soon
	// as the last lease is released".
	IdleTimeout time.Duration

	// KillTimeout is the grace period between SIGTERM and SIGKILL during
	// teardown. Zero means DefaultKillTimeout.
	KillTimeout time.Duration

	// StderrLines caps the retained stderr tail used to explain a failed
	// boot. Zero means DefaultStderrLines.
	StderrLines int

	// Logger receives lifecycle and stderr records. Zero means the
	// process-wide background logger.
	Logger *slog.Logger
}

func (s ServerSpec) validate() error {
	if strings.TrimSpace(s.Key) == "" {
		return fmt.Errorf("enginehost: spec key required")
	}
	if strings.TrimSpace(s.Binary) == "" {
		return fmt.Errorf("enginehost: spec binary required")
	}
	if s.Ready == nil {
		return fmt.Errorf("enginehost: spec ready probe required")
	}
	return nil
}

func (s ServerSpec) readyTimeout() time.Duration {
	if s.ReadyTimeout <= 0 {
		return DefaultReadyTimeout
	}
	return s.ReadyTimeout
}

func (s ServerSpec) idleTimeout() time.Duration {
	if s.IdleTimeout == 0 {
		return DefaultIdleTimeout
	}
	return s.IdleTimeout
}

func (s ServerSpec) killTimeout() time.Duration {
	if s.KillTimeout <= 0 {
		return DefaultKillTimeout
	}
	return s.KillTimeout
}
