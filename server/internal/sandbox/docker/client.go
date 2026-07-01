package docker

import (
	"bytes"
	"context"
	"errors"
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
	runner      runnerFunc
}

func (c *Client) Create(ctx context.Context, input e2b.CreateInput) (e2b.Sandbox, error) {
	args := []string{"run", "-d", "--entrypoint", "sleep"}
	if strings.TrimSpace(c.Network) != "" {
		args = append(args, "--network", c.Network)
	}
	if c.HostGateway {
		args = append(args, "--add-host", "host.docker.internal:host-gateway")
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
	return e2b.Sandbox{SandboxID: strings.TrimSpace(res.Stdout)}, nil
}

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
	_, err := c.runnerOrDefault()(ctx, "docker", []string{"rm", "-f", sandboxID}, nil)
	return err
}

func (c *Client) Renew(_ context.Context, _ string, _ int) error {
	return nil
}

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
