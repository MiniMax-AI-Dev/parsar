package docker

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/sandbox/e2b"
)

// TestIntegrationRealDockerLifecycle drives the real os/exec runner against a
// live docker daemon. Skipped unless PARSAR_DOCKER_IT=1 (and the
// parsar-sandbox:local image exists) so `go test ./...` stays hermetic.
//
// Run: PARSAR_DOCKER_IT=1 go test ./server/internal/sandbox/docker/ -run Integration -v
func TestIntegrationRealDockerLifecycle(t *testing.T) {
	if os.Getenv("PARSAR_DOCKER_IT") != "1" {
		t.Skip("set PARSAR_DOCKER_IT=1 to run the real-docker integration test")
	}
	image := os.Getenv("PARSAR_DOCKER_IT_IMAGE")
	if image == "" {
		image = "parsar-sandbox:local"
	}

	client := &Client{Image: image}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	sb, err := client.Create(ctx, e2b.CreateInput{
		Env:      map[string]string{"FOO": "bar"},
		Metadata: map[string]string{"parsar.test": "it"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if strings.TrimSpace(sb.SandboxID) == "" {
		t.Fatalf("expected non-empty container id")
	}
	t.Logf("created container %s", sb.SandboxID)

	defer func() {
		if err := client.Kill(context.Background(), sb.SandboxID); err != nil {
			t.Errorf("kill: %v", err)
		}
		out, _ := osExecRun(context.Background(), "docker",
			[]string{"ps", "-a", "--filter", "id=" + sb.SandboxID, "--format", "{{.ID}}"}, nil)
		if strings.TrimSpace(out.Stdout) != "" {
			t.Errorf("expected container removed, still present: %q", out.Stdout)
		}
	}()

	res, err := client.RunCommand(ctx, e2b.RunCommandInput{
		Sandbox: sb,
		Command: "echo hi-$FOO",
		CWD:     "/workspace",
		Env:     map[string]string{"FOO": "bar"},
	})
	if err != nil {
		t.Fatalf("run echo: %v", err)
	}
	if res.Status != "0" || !res.Exited {
		t.Fatalf("expected exit 0, got status=%q exited=%v stderr=%q", res.Status, res.Exited, res.Stderr)
	}
	if strings.TrimSpace(res.Stdout) != "hi-bar" {
		t.Fatalf("expected 'hi-bar', got %q", res.Stdout)
	}

	// Non-zero exit must surface as Status, not a Go error.
	res, err = client.RunCommand(ctx, e2b.RunCommandInput{Sandbox: sb, Command: "exit 7"})
	if err != nil {
		t.Fatalf("run exit7 returned error, want nil: %v", err)
	}
	if res.Status != "7" {
		t.Fatalf("expected status 7, got %q", res.Status)
	}

	res, err = client.RunCommand(ctx, e2b.RunCommandInput{Sandbox: sb, Command: "parsar-daemon version"})
	if err != nil {
		t.Fatalf("run daemon version: %v", err)
	}
	if res.Status != "0" {
		t.Fatalf("expected daemon version exit 0, got %q stderr=%q", res.Status, res.Stderr)
	}
	t.Logf("parsar-daemon version -> %q", strings.TrimSpace(res.Stdout))
}
