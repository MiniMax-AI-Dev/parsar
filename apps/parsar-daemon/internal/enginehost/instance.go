package enginehost

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/clirunner"
	obslog "github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
)

// DefaultStderrLines is how many trailing stderr lines an instance keeps
// so a readiness failure can be explained without a log dive.
const DefaultStderrLines = 40

// LoopbackHost is the only interface an engine server is ever bound to.
// These servers authenticate nobody: the engines this package supervises
// gate requests on "the peer is on loopback" and nothing else, so binding
// any other interface would publish an unauthenticated agent runtime to
// the network.
const LoopbackHost = "127.0.0.1"

// instance is one resident engine server. It is created ready or not at
// all: newInstance returns only after the spec's Ready probe passes, so
// callers never observe a half-booted server.
type instance struct {
	key     string
	port    int
	baseURL string
	proc    *clirunner.Process

	stderr *lineTail

	// leases counts live Lease values. Guarded by the supervisor's mutex,
	// not by the instance, because lease transitions and the map lookup
	// that finds this instance have to be one atomic step.
	leases int

	// idleTimer fires the reclamation when leases hits zero. Also guarded
	// by the supervisor's mutex.
	idleTimer *time.Timer

	stopOnce sync.Once
	exited   chan struct{}
}

// newInstance allocates a port, prepares state, launches, and waits for
// readiness. Every failure path leaves no process running.
func newInstance(ctx context.Context, spec ServerSpec) (*instance, error) {
	logger := spec.Logger
	if logger == nil {
		logger = obslog.Bg()
	}

	port, err := freeLoopbackPort()
	if err != nil {
		return nil, err
	}
	if spec.Prepare != nil {
		if err := spec.Prepare(ctx, port); err != nil {
			return nil, fmt.Errorf("enginehost: prepare %s: %w", spec.Key, err)
		}
	}

	var args []string
	if spec.Args != nil {
		args = spec.Args(port)
	}
	var env []string
	if spec.Env != nil {
		env = spec.Env(port)
	}

	// The process is deliberately parented to context.Background(): it
	// outlives the prompt that launched it, and its lifetime is owned by
	// lease counting and Stop, not by any one caller's context.
	proc, err := clirunner.Start(clirunner.StartOptions{
		Parent:      context.Background(),
		Binary:      spec.Binary,
		Args:        args,
		Dir:         spec.Dir,
		Env:         env,
		KillTimeout: spec.killTimeout(),
	})
	if err != nil {
		return nil, fmt.Errorf("enginehost: start %s: %w", spec.Binary, err)
	}

	lines := spec.StderrLines
	if lines <= 0 {
		lines = DefaultStderrLines
	}
	inst := &instance{
		key:     spec.Key,
		port:    port,
		baseURL: "http://" + LoopbackHost + ":" + strconv.Itoa(port),
		proc:    proc,
		stderr:  newLineTail(lines),
		exited:  make(chan struct{}),
	}

	go inst.drain(proc.Stderr, logger, "stderr")
	// Engine servers log to stdout too; draining it prevents a full pipe
	// from wedging the process, and the tail helps diagnose a bad boot.
	go inst.drain(proc.Stdout, logger, "stdout")
	go func() {
		waitErr := proc.Wait()
		close(inst.exited)
		logger.Info("enginehost: engine server exited", "key", spec.Key, "port", port, "err", waitErr)
	}()

	if err := inst.awaitReady(ctx, spec); err != nil {
		inst.stop()
		return nil, err
	}
	logger.Info("enginehost: engine server ready", "key", spec.Key, "port", port)
	return inst, nil
}

// awaitReady polls the spec probe until it passes, the deadline expires,
// or the process dies. A dead process short-circuits: waiting out the
// full ready timeout on a process that already exited only delays the
// error the caller needs.
func (i *instance) awaitReady(ctx context.Context, spec ServerSpec) error {
	deadline := time.Now().Add(spec.readyTimeout())
	var lastErr error
	for {
		select {
		case <-i.exited:
			return fmt.Errorf("enginehost: %s exited before becoming ready: %s", spec.Key, i.stderr.String())
		case <-ctx.Done():
			return fmt.Errorf("enginehost: %s readiness cancelled: %w", spec.Key, ctx.Err())
		default:
		}

		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		lastErr = spec.Ready(probeCtx, i.baseURL)
		cancel()
		if lastErr == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("enginehost: %s not ready within %s: %w (stderr: %s)",
				spec.Key, spec.readyTimeout(), lastErr, i.stderr.String())
		}
		select {
		case <-time.After(250 * time.Millisecond):
		case <-i.exited:
		case <-ctx.Done():
		}
	}
}

func (i *instance) drain(r io.Reader, logger *slog.Logger, stream string) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 16*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		i.stderr.push(line)
		logger.Debug("enginehost: engine server output", "key", i.key, "stream", stream, "line", line)
	}
}

// alive reports whether the process is still running.
func (i *instance) alive() bool {
	select {
	case <-i.exited:
		return false
	default:
		return true
	}
}

// stop terminates the process. Idempotent.
func (i *instance) stop() {
	i.stopOnce.Do(func() { i.proc.Cancel() })
}

// freeLoopbackPort asks the kernel for an unused loopback port and
// releases it immediately so the engine can bind it.
//
// This hands the port over through a close/bind gap rather than passing a
// listening socket, because these engines bind their own listener from a
// config value. The gap is a real (if narrow) race; a lost race surfaces
// as a boot failure with the engine's own "address in use" on the stderr
// tail, which the caller retries at the next prompt.
func freeLoopbackPort() (int, error) {
	l, err := net.Listen("tcp", net.JoinHostPort(LoopbackHost, "0"))
	if err != nil {
		return 0, fmt.Errorf("enginehost: reserve loopback port: %w", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		return 0, fmt.Errorf("enginehost: release reserved port %d: %w", port, err)
	}
	return port, nil
}
