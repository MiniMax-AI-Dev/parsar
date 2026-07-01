package docker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/sandbox/e2b"
)

// syntheticTTL is the fake expiry GetInfo reports. Local docker containers
// have no control-plane TTL; the provider only uses EndAt as optional
// status metadata, so any comfortably-future value is fine.
const syntheticTTL = 30 * 24 * time.Hour

type execResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// runnerFunc is the injection seam: production wires an os/exec-backed
// runner, tests wire a fake so no real docker daemon is required.
type runnerFunc func(ctx context.Context, name string, args []string, stdin io.Reader) (execResult, error)

type Client struct {
	Image       string
	Network     string
	HostGateway bool
	// Optional container resource limits, passed straight to `docker run`
	// when non-empty (empty = flag omitted, docker's default). A malformed
	// value makes `docker run` exit non-zero, which Create surfaces as an
	// error, so no pre-validation is needed here.
	Memory    string // --memory, e.g. "2g"
	CPUs      string // --cpus, e.g. "1.5"
	PidsLimit string // --pids-limit, e.g. "512"
	runner    runnerFunc
}

func (c *Client) Create(ctx context.Context, input e2b.CreateInput) (e2b.Sandbox, error) {
	args := []string{"run", "-d", "--entrypoint", "sleep"}
	if strings.TrimSpace(c.Network) != "" {
		args = append(args, "--network", c.Network)
	}
	if c.HostGateway {
		args = append(args, "--add-host", "host.docker.internal:host-gateway")
	}
	if m := strings.TrimSpace(c.Memory); m != "" {
		args = append(args, "--memory", m)
	}
	if cpus := strings.TrimSpace(c.CPUs); cpus != "" {
		args = append(args, "--cpus", cpus)
	}
	if pids := strings.TrimSpace(c.PidsLimit); pids != "" {
		args = append(args, "--pids-limit", pids)
	}
	for k, v := range input.Metadata {
		args = append(args, "--label", k+"="+v)
	}
	for k, v := range input.Env {
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, c.Image, "infinity")

	res, err := c.runnerOrDefault()(ctx, "docker", args, nil)
	if err != nil {
		return e2b.Sandbox{}, err
	}
	// Create is a control-plane verb: unlike RunCommand, a non-zero docker
	// exit means the operation failed, not a payload. Surface it (with
	// stderr) instead of returning an empty-id sandbox that reads as success.
	if res.ExitCode != 0 {
		return e2b.Sandbox{}, fmt.Errorf("dockersandbox: docker run failed (exit %d): %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	id := strings.TrimSpace(res.Stdout)
	if id == "" {
		return e2b.Sandbox{}, fmt.Errorf("dockersandbox: docker run returned no container id (stderr: %s)", strings.TrimSpace(res.Stderr))
	}
	return e2b.Sandbox{SandboxID: id}, nil
}

// RunCommand execs into the container and returns the command's exit code as
// Status (Create/Kill treat non-zero as failure; here it's the payload). Note
// a docker-level launch failure — e.g. the container is gone — surfaces as
// docker exec's own 125/126/127, indistinguishable from the command exiting
// with those codes; callers see it as a failed command with docker's stderr.
func (c *Client) RunCommand(ctx context.Context, input e2b.RunCommandInput) (e2b.CommandResult, error) {
	args := []string{"exec"}
	if cwd := strings.TrimSpace(input.CWD); cwd != "" {
		args = append(args, "-w", cwd)
	}
	user := strings.TrimSpace(input.User)
	if user == "" {
		user = "root"
	}
	args = append(args, "-u", user)
	for k, v := range input.Env {
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, input.Sandbox.SandboxID, "/bin/bash", "-l", "-c", input.Command)

	res, err := c.runnerOrDefault()(ctx, "docker", args, nil)
	if err != nil {
		return e2b.CommandResult{}, err
	}
	return e2b.CommandResult{
		Stdout: res.Stdout,
		Stderr: res.Stderr,
		Status: strconv.Itoa(res.ExitCode),
		Exited: true,
	}, nil
}

func (c *Client) Kill(ctx context.Context, sandboxID string) error {
	sandboxID = strings.TrimSpace(sandboxID)
	if sandboxID == "" {
		return errors.New("dockersandbox: sandbox id is empty")
	}
	res, err := c.runnerOrDefault()(ctx, "docker", []string{"rm", "-f", sandboxID}, nil)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		// Removing an already-gone container is idempotent success; any other
		// non-zero exit (daemon unreachable, permission denied) must surface
		// so Release/Reap don't record a leaked container as killed.
		if strings.Contains(res.Stderr, "No such container") {
			return nil
		}
		return fmt.Errorf("dockersandbox: docker rm -f %s failed (exit %d): %s", sandboxID, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return nil
}

func (c *Client) Renew(_ context.Context, _ string, _ int) error {
	return nil
}

// GetInfo returns synthetic metadata: local containers have no control-plane,
// so State is always "running" and EndAt a fixed future TTL. It is NOT a
// liveness probe — a crashed container still reports "running". The provider
// only consumes EndAt (optional status metadata), so this is sufficient.
func (c *Client) GetInfo(_ context.Context, sandboxID string) (e2b.SandboxRuntimeInfo, error) {
	now := time.Now()
	return e2b.SandboxRuntimeInfo{
		SandboxID: strings.TrimSpace(sandboxID),
		StartedAt: now,
		EndAt:     now.Add(syntheticTTL),
		State:     "running",
	}, nil
}

func (c *Client) runnerOrDefault() runnerFunc {
	if c.runner != nil {
		return c.runner
	}
	return osExecRun
}

// osExecRun runs a local process. A non-zero exit is a normal result
// (ExitCode set, err nil) so RunCommand can report it as Status; only a
// launch failure or context cancellation returns a non-nil error.
func osExecRun(ctx context.Context, name string, args []string, stdin io.Reader) (execResult, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	res := execResult{Stdout: stdout.String(), Stderr: stderr.String()}
	if err != nil {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
			return res, nil
		}
		return res, err
	}
	return res, nil
}
