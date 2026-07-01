package docker

import (
	"context"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/sandbox/e2b"
)

type recordedCall struct {
	Name  string
	Args  []string
	Stdin string
}

// fakeRunner records calls and returns canned output so unit tests never
// touch a real docker daemon.
type fakeRunner struct {
	calls   []recordedCall
	handler func(call recordedCall) (execResult, error)
}

func (f *fakeRunner) run(_ context.Context, name string, args []string, stdin io.Reader) (execResult, error) {
	var stdinStr string
	if stdin != nil {
		b, _ := io.ReadAll(stdin)
		stdinStr = string(b)
	}
	call := recordedCall{Name: name, Args: args, Stdin: stdinStr}
	f.calls = append(f.calls, call)
	if f.handler != nil {
		return f.handler(call)
	}
	return execResult{}, nil
}

func containsArg(args []string, want string) bool {
	return slices.Contains(args, want)
}

func TestCreateRunsDockerRunAndReturnsContainerID(t *testing.T) {
	fake := &fakeRunner{
		handler: func(recordedCall) (execResult, error) {
			// docker run -d prints the full container ID on stdout.
			return execResult{Stdout: "abc123def456\n"}, nil
		},
	}
	client := &Client{Image: "parsar-sandbox:local", runner: fake.run}

	sb, err := client.Create(context.Background(), e2b.CreateInput{})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if sb.SandboxID != "abc123def456" {
		t.Fatalf("expected container id as sandbox id, got %q", sb.SandboxID)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected exactly 1 docker call, got %d", len(fake.calls))
	}
	call := fake.calls[0]
	if call.Name != "docker" {
		t.Fatalf("expected docker binary, got %q", call.Name)
	}
	if !containsArg(call.Args, "run") || !containsArg(call.Args, "-d") {
		t.Fatalf("expected `docker run -d`, got args %v", call.Args)
	}
	if !containsArg(call.Args, "parsar-sandbox:local") {
		t.Fatalf("expected image in args, got %v", call.Args)
	}
}

func TestCreateKeepsContainerAliveViaEntrypointOverride(t *testing.T) {
	fake := &fakeRunner{handler: func(recordedCall) (execResult, error) {
		return execResult{Stdout: "cid\n"}, nil
	}}
	client := &Client{Image: "img", runner: fake.run}

	if _, err := client.Create(context.Background(), e2b.CreateInput{}); err != nil {
		t.Fatalf("create: %v", err)
	}
	call := fake.calls[0]
	// The container must outlive `docker run` so later `docker exec` works;
	// the e2b base image's default entrypoint is envd, which we bypass
	// locally in favour of exec, so we override it with a keepalive.
	if !containsArg(call.Args, "--entrypoint") {
		t.Fatalf("expected --entrypoint override, got %v", call.Args)
	}
	if !strings.Contains(strings.Join(call.Args, " "), "sleep") {
		t.Fatalf("expected keepalive command, got %v", call.Args)
	}
}

func TestCreateAppliesNetworkHostGatewayEnvAndLabels(t *testing.T) {
	var got recordedCall
	fake := &fakeRunner{handler: func(call recordedCall) (execResult, error) {
		got = call
		return execResult{Stdout: "cid\n"}, nil
	}}
	client := &Client{
		Image:       "img",
		Network:     "parsar_default",
		HostGateway: true,
		runner:      fake.run,
	}
	_, err := client.Create(context.Background(), e2b.CreateInput{
		Env:      map[string]string{"FOO": "bar"},
		Metadata: map[string]string{"workspace": "ws1"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	joined := strings.Join(got.Args, " ")
	if !strings.Contains(joined, "--network parsar_default") {
		t.Fatalf("expected --network, got %v", got.Args)
	}
	if !strings.Contains(joined, "--add-host host.docker.internal:host-gateway") {
		t.Fatalf("expected host-gateway mapping, got %v", got.Args)
	}
	if !strings.Contains(joined, "-e FOO=bar") {
		t.Fatalf("expected env passthrough, got %v", got.Args)
	}
	if !strings.Contains(joined, "--label workspace=ws1") {
		t.Fatalf("expected metadata label, got %v", got.Args)
	}
}

func TestKillRemovesContainer(t *testing.T) {
	fake := &fakeRunner{}
	client := &Client{Image: "img", runner: fake.run}

	if err := client.Kill(context.Background(), "cid42"); err != nil {
		t.Fatalf("kill: %v", err)
	}
	call := fake.calls[0]
	if !containsArg(call.Args, "rm") || !containsArg(call.Args, "-f") || !containsArg(call.Args, "cid42") {
		t.Fatalf("expected `docker rm -f cid42`, got %v", call.Args)
	}
}

func TestKillEmptyIDIsError(t *testing.T) {
	fake := &fakeRunner{}
	client := &Client{Image: "img", runner: fake.run}
	if err := client.Kill(context.Background(), "  "); err == nil {
		t.Fatalf("expected error for empty sandbox id")
	}
	if len(fake.calls) != 0 {
		t.Fatalf("expected no docker call for empty id, got %v", fake.calls)
	}
}

func TestRenewIsNoOp(t *testing.T) {
	fake := &fakeRunner{}
	client := &Client{Image: "img", runner: fake.run}
	if err := client.Renew(context.Background(), "cid", 60); err != nil {
		t.Fatalf("renew: %v", err)
	}
	// Local containers have no control-plane TTL, so Renew must not shell
	// out — it exists only to satisfy the E2BClient interface.
	if len(fake.calls) != 0 {
		t.Fatalf("expected renew to be a no-op, got calls %v", fake.calls)
	}
}

func TestGetInfoReturnsSyntheticFutureExpiry(t *testing.T) {
	fake := &fakeRunner{}
	client := &Client{Image: "img", runner: fake.run}
	before := time.Now()
	info, err := client.GetInfo(context.Background(), "cid99")
	if err != nil {
		t.Fatalf("getinfo: %v", err)
	}
	if info.SandboxID != "cid99" {
		t.Fatalf("expected sandbox id echoed, got %q", info.SandboxID)
	}
	if !info.EndAt.After(before) {
		t.Fatalf("expected future EndAt, got %v", info.EndAt)
	}
	if info.State != "running" {
		t.Fatalf("expected running state, got %q", info.State)
	}
}

func TestOSExecRunCapturesStdoutAndZeroExit(t *testing.T) {
	res, err := osExecRun(context.Background(), "sh", []string{"-c", "printf hello"}, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Stdout != "hello" {
		t.Fatalf("stdout=%q", res.Stdout)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit=%d, want 0", res.ExitCode)
	}
}

func TestOSExecRunCapturesNonZeroExitWithoutError(t *testing.T) {
	// A command exiting non-zero is a normal result, not a runner failure:
	// RunCommand must surface it as CommandResult.Status, so err stays nil.
	res, err := osExecRun(context.Background(), "sh", []string{"-c", "printf oops >&2; exit 7"}, nil)
	if err != nil {
		t.Fatalf("expected nil err for clean non-zero exit, got %v", err)
	}
	if res.ExitCode != 7 {
		t.Fatalf("exit=%d, want 7", res.ExitCode)
	}
	if res.Stderr != "oops" {
		t.Fatalf("stderr=%q", res.Stderr)
	}
}

func TestNilRunnerFallsBackToOSExec(t *testing.T) {
	if (&Client{}).runnerOrDefault() == nil {
		t.Fatalf("expected non-nil default runner when none injected")
	}
}

func TestRunCommandExecsBashWrapperAndReturnsExitStatus(t *testing.T) {
	fake := &fakeRunner{
		handler: func(recordedCall) (execResult, error) {
			return execResult{Stdout: "hi\n", ExitCode: 0}, nil
		},
	}
	client := &Client{Image: "img", runner: fake.run}

	res, err := client.RunCommand(context.Background(), e2b.RunCommandInput{
		Sandbox: e2b.Sandbox{SandboxID: "cid789"},
		Command: "echo hi",
	})
	if err != nil {
		t.Fatalf("run command: %v", err)
	}
	if res.Stdout != "hi\n" {
		t.Fatalf("expected stdout hi, got %q", res.Stdout)
	}
	if !res.Exited || res.Status != "0" {
		t.Fatalf("expected exited=true status=0, got exited=%v status=%q", res.Exited, res.Status)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(fake.calls))
	}
	call := fake.calls[0]
	if !containsArg(call.Args, "exec") {
		t.Fatalf("expected `docker exec`, got %v", call.Args)
	}
	if !containsArg(call.Args, "cid789") {
		t.Fatalf("expected container id in args, got %v", call.Args)
	}
	// The command must run through a login bash so PATH + profile match e2b's
	// `/bin/bash -l -c` contract that seed/connect scripts rely on.
	joined := strings.Join(call.Args, " ")
	if !strings.Contains(joined, "/bin/bash -l -c") {
		t.Fatalf("expected bash login wrapper, got %v", call.Args)
	}
	if call.Args[len(call.Args)-1] != "echo hi" {
		t.Fatalf("expected raw command as final arg, got %q", call.Args[len(call.Args)-1])
	}
}

func TestCreateReturnsErrorOnNonZeroExit(t *testing.T) {
	// `docker run` failing (image missing, daemon down, bad --network) exits
	// non-zero with an empty stdout. Create is a control-plane verb, so this
	// must be a Go error — not a Sandbox{SandboxID:""} reported as success —
	// and the error must carry docker's stderr for diagnosis.
	fake := &fakeRunner{handler: func(recordedCall) (execResult, error) {
		return execResult{ExitCode: 125, Stderr: "Unable to find image 'missing:tag' locally\nno such image"}, nil
	}}
	client := &Client{Image: "missing:tag", runner: fake.run}

	sb, err := client.Create(context.Background(), e2b.CreateInput{})
	if err == nil {
		t.Fatalf("expected error on non-zero docker run exit, got sandbox %+v", sb)
	}
	if !strings.Contains(err.Error(), "no such image") {
		t.Fatalf("expected docker stderr in error, got %q", err.Error())
	}
}

func TestCreateReturnsErrorOnEmptyContainerID(t *testing.T) {
	// A zero exit but blank stdout still yields no usable container id; the
	// provider must not proceed with an empty SandboxID.
	fake := &fakeRunner{handler: func(recordedCall) (execResult, error) {
		return execResult{ExitCode: 0, Stdout: "  \n"}, nil
	}}
	client := &Client{Image: "img", runner: fake.run}

	if sb, err := client.Create(context.Background(), e2b.CreateInput{}); err == nil {
		t.Fatalf("expected error on empty container id, got sandbox %+v", sb)
	}
}

func TestKillReturnsErrorOnNonZeroExit(t *testing.T) {
	// A real `docker rm -f` failure (e.g. daemon unreachable) must surface so
	// Release/Reap don't record a leaked container as successfully killed.
	fake := &fakeRunner{handler: func(recordedCall) (execResult, error) {
		return execResult{ExitCode: 1, Stderr: "Cannot connect to the Docker daemon at unix:///var/run/docker.sock"}, nil
	}}
	client := &Client{Image: "img", runner: fake.run}

	err := client.Kill(context.Background(), "cid42")
	if err == nil {
		t.Fatalf("expected error on non-zero docker rm exit")
	}
	if !strings.Contains(err.Error(), "Cannot connect to the Docker daemon") {
		t.Fatalf("expected docker stderr in error, got %q", err.Error())
	}
}

func TestKillToleratesMissingContainer(t *testing.T) {
	// Kill must stay idempotent: removing an already-gone container is a
	// success, not an error, so retries/Reap don't wedge on it.
	fake := &fakeRunner{handler: func(recordedCall) (execResult, error) {
		return execResult{ExitCode: 1, Stderr: "Error: No such container: cid42"}, nil
	}}
	client := &Client{Image: "img", runner: fake.run}

	if err := client.Kill(context.Background(), "cid42"); err != nil {
		t.Fatalf("expected nil error when container already gone, got %v", err)
	}
}

func TestCreateAppliesResourceLimitsWhenSet(t *testing.T) {
	var got recordedCall
	fake := &fakeRunner{handler: func(call recordedCall) (execResult, error) {
		got = call
		return execResult{Stdout: "cid\n"}, nil
	}}
	client := &Client{
		Image:     "img",
		Memory:    "2g",
		CPUs:      "1.5",
		PidsLimit: "512",
		runner:    fake.run,
	}
	if _, err := client.Create(context.Background(), e2b.CreateInput{}); err != nil {
		t.Fatalf("create: %v", err)
	}
	joined := strings.Join(got.Args, " ")
	for _, want := range []string{"--memory 2g", "--cpus 1.5", "--pids-limit 512"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected %q in args, got %v", want, got.Args)
		}
	}
}

func TestCreateOmitsResourceLimitsWhenUnset(t *testing.T) {
	// Defaults must be byte-for-byte unchanged: no limit flags unless the
	// operator opts in via env.
	var got recordedCall
	fake := &fakeRunner{handler: func(call recordedCall) (execResult, error) {
		got = call
		return execResult{Stdout: "cid\n"}, nil
	}}
	client := &Client{Image: "img", runner: fake.run}
	if _, err := client.Create(context.Background(), e2b.CreateInput{}); err != nil {
		t.Fatalf("create: %v", err)
	}
	joined := strings.Join(got.Args, " ")
	for _, unwanted := range []string{"--memory", "--cpus", "--pids-limit"} {
		if strings.Contains(joined, unwanted) {
			t.Fatalf("expected no %q flag when unset, got %v", unwanted, got.Args)
		}
	}
}
