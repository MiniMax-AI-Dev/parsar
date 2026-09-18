package docker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/containerd/errdefs"
	"github.com/google/uuid"
	"github.com/moby/moby/client"
)

func TestProviderRejectsUnsafeOperatorConfiguration(t *testing.T) {
	c, e := client.New()
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	base := Config{InstallationID: uuid.NewString(), Image: "test@sha256:" + strings.Repeat("a", 64), Network: "bridge", Seccomp: `{}`}
	for _, change := range []func(*Config){func(c *Config) { c.Image = "mutable:latest" }, func(c *Config) { c.InstallationID = "" }, func(c *Config) { c.Network = "host" }, func(c *Config) { c.Network = "container:other" }, func(c *Config) { c.Seccomp = "" }} {
		v := base
		change(&v)
		if _, e := New(c, v); !errors.Is(e, sandbox.ErrInvalid) {
			t.Fatalf("accepted invalid configuration: %v", e)
		}
	}
}

// This optional Docker mechanism test uses a pinned fixture image whose entrypoint
// is sleep. It is not native/model acceptance; the real Runtime has separate checks.
func TestDockerProviderLifecycle(t *testing.T) {
	image := os.Getenv("AGENTS_RUNTIME_DOCKER_TEST_IMAGE")
	if image == "" {
		t.Skip("explicit Docker fixture image required")
	}
	seccomp, e := os.ReadFile("../../../deploy/codex/seccomp.json")
	if e != nil {
		t.Fatal(e)
	}
	c, e := client.New(client.FromEnv)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	p, e := New(c, Config{InstallationID: uuid.NewString(), Image: image, Network: "bridge", Seccomp: string(seccomp)})
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	bootstrap := func() sandbox.Bootstrap {
		return sandbox.Bootstrap{Reference: sandbox.Reference{TenantID: uuid.NewString(), EnvironmentID: uuid.NewString(), AllocationID: uuid.NewString()}, SessionID: uuid.NewString(), DeviceID: uuid.NewString(), CoreURL: "http://core.invalid/api/v1", Credential: "synthetic-test-credential"}
	}
	b := bootstrap()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if e := p.Kill(ctx, b.Reference); e != nil {
			t.Error(e)
		}
	})
	info, e := p.Create(ctx, b)
	if e != nil {
		t.Fatal(e)
	}
	if info.State != "running" || info.ProviderID == "" {
		t.Fatalf("bad compute observation: %+v", info)
	}
	inspected, e := p.inspect(ctx, b.Reference)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(inspected.Raw), b.Credential) || inspected.Container.Config.User != "1000:1000" || !inspected.Container.HostConfig.ReadonlyRootfs || inspected.Container.HostConfig.Privileged {
		t.Fatal("unsafe Docker configuration")
	}
	changed := b
	changed.Credential = "must-not-replace-existing"
	if _, e = p.Create(ctx, changed); !errors.Is(e, sandbox.ErrExists) {
		t.Fatalf("duplicate not rejected: %v", e)
	}
	r, e := p.RunCommand(ctx, b.Reference, sandbox.Command{Args: []string{"cat", "/home/runtime/.parsar/parsar-daemon/default/auth.json"}})
	if e != nil {
		t.Fatal(e)
	}
	var auth map[string]string
	if json.Unmarshal([]byte(r.Stdout), &auth) != nil || auth["runner_credential"] != b.Credential || auth["runtime_id"] != b.DeviceID {
		t.Fatal("bootstrap changed or malformed")
	}
	r, e = p.RunCommand(ctx, b.Reference, sandbox.Command{Args: []string{"sh", "-c", "printf retained > /environment/workspace/history; printf failed >&2; exit 7"}})
	if e != nil || r.ExitCode != 7 || r.Stderr != "failed" {
		t.Fatalf("lost command status: %+v %v", r, e)
	}
	r, e = p.RunCommand(ctx, b.Reference, sandbox.Command{Args: []string{"sh", "-c", "set -eu; test \"$(cat /workspace/history)\" = retained; printf replaced > /environment/staging/replacement; mv /environment/staging/replacement /environment/workspace/history; cat /workspace/history"}})
	if e != nil || r.ExitCode != 0 || r.Stdout != "replaced" {
		t.Fatal("public workspace view or atomic staging failed", e)
	}
	wrong := b.Reference
	wrong.TenantID = uuid.NewString()
	if _, e = p.GetInfo(ctx, wrong); !errors.Is(e, sandbox.ErrNotFound) {
		t.Fatal("foreign allocation visible")
	}
	if e = p.Kill(ctx, wrong); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Renew(ctx, b.Reference); e != nil {
		t.Fatal("wrong tenant removed the owner")
	}
	timeout := 1
	if _, e = c.ContainerRestart(ctx, info.ProviderID, client.ContainerRestartOptions{Timeout: &timeout}); e != nil {
		t.Fatal(e)
	}
	r, e = p.RunCommand(ctx, b.Reference, sandbox.Command{Args: []string{"cat", "/environment/workspace/history"}})
	if e != nil || r.Stdout != "replaced" {
		t.Fatal("restart lost workspace")
	}
	r, e = p.RunCommand(ctx, b.Reference, sandbox.Command{Args: []string{"sh", "-c", "touch /cannot-write-root"}})
	if e != nil || r.ExitCode == 0 {
		t.Fatal("root filesystem writable")
	}
	// Container loss must not trigger credential overwrite or state replacement.
	if _, e = c.ContainerRemove(ctx, info.ProviderID, client.ContainerRemoveOptions{Force: true}); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Create(ctx, b); !errors.Is(e, sandbox.ErrExists) {
		t.Fatalf("retained volumes reused: %v", e)
	}
	if e = p.Kill(ctx, b.Reference); e != nil {
		t.Fatal(e)
	}
	for _, suffix := range []string{"-home", "-environment"} {
		if _, e = c.VolumeInspect(ctx, p.name(b.Reference)+suffix, client.VolumeInspectOptions{}); !errdefs.IsNotFound(e) {
			t.Fatal("named volume remains")
		}
	}
	// A colliding resource with different ownership cannot be deleted, including
	// when its container is absent after a partial creation.
	foreign := bootstrap()
	volume := p.name(foreign.Reference) + "-home"
	if _, e = c.VolumeCreate(ctx, client.VolumeCreateOptions{Name: volume, Labels: map[string]string{"fixture": "foreign"}}); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		_, e := c.VolumeRemove(context.Background(), volume, client.VolumeRemoveOptions{})
		if e != nil {
			t.Error(e)
		}
	})
	if e = p.Kill(ctx, foreign.Reference); !errors.Is(e, sandbox.ErrOwnership) {
		t.Fatal("foreign volume cleanup accepted")
	}
	if _, e = p.Create(ctx, foreign); !errors.Is(e, sandbox.ErrOwnership) {
		t.Fatal("foreign volume bootstrap accepted")
	}
	// Closing initialization output is not process termination. Require explicit
	// reclamation, without returning partial output as a successful command.
	next := bootstrap()
	defer p.Kill(context.Background(), next.Reference)
	if _, e = p.Create(ctx, next); e != nil {
		t.Fatal(e)
	}
	short, stop := context.WithTimeout(ctx, 100*time.Millisecond)
	_, e = p.RunCommand(short, next.Reference, sandbox.Command{Args: []string{"sleep", "30"}})
	stop()
	if !errors.Is(e, sandbox.ErrCommandUnconfirmed) {
		t.Fatalf("timeout classified as certain: %v", e)
	}
	if e = p.Kill(ctx, next.Reference); e != nil {
		t.Fatal(e)
	}
}
