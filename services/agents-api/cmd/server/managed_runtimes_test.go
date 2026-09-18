package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestManagedRuntimeOperatorConfigurationIsExplicit(t *testing.T) {
	t.Setenv("AGENTS_API_MANAGED_RUNTIMES_FILE", "")
	if result, close, err := managedRuntimes(); err != nil || result != nil {
		t.Fatal("implicit managed deployment", err)
	} else {
		close()
	}
	root := t.TempDir()
	seccomp := filepath.Join(root, "seccomp.json")
	file := filepath.Join(root, "providers.json")
	if err := os.WriteFile(seccomp, []byte(`{"defaultAction":"SCMP_ACT_ERRNO","syscalls":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	key := uuid.NewString()
	config := managedRuntimeConfig{CoreURL: "http://core.example/api/v1", DefaultProvider: key, Docker: map[string]managedDockerConfig{key: {Host: "unix:///var/run/docker.sock", Image: "sha256:" + strings.Repeat("a", 64), Network: "bridge", SeccompFile: seccomp}}}
	write := func(c managedRuntimeConfig) {
		raw, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(config)
	t.Setenv("AGENTS_API_MANAGED_RUNTIMES_FILE", file)
	t.Setenv("AGENTS_API_DAEMON_WS_URL", "")
	if _, close, err := managedRuntimes(); err == nil {
		close()
		t.Fatal("managed configuration without authenticated daemon gateway accepted")
	}
	t.Setenv("AGENTS_API_DAEMON_WS_URL", "ws://core.example/api/v1/agent-daemon/ws")
	t.Setenv("DOCKER_HOST", "not-a-valid-ambient-endpoint")
	result, close, err := managedRuntimes()
	if err != nil {
		t.Fatal(err)
	}
	defer close()
	if result.DefaultProvider != key || len(result.Providers) != 1 || result.Providers[key] == nil {
		t.Fatal("provider identity lost")
	}
	for _, mutate := range []func(*managedRuntimeConfig){
		func(c *managedRuntimeConfig) { c.DefaultProvider = uuid.NewString() },
		func(c *managedRuntimeConfig) { e := c.Docker[key]; e.Host = "tcp://remote:2375"; c.Docker[key] = e },
		func(c *managedRuntimeConfig) { e := c.Docker[key]; e.Image = "latest"; c.Docker[key] = e },
		func(c *managedRuntimeConfig) { e := c.Docker[key]; e.Network = "host"; c.Docker[key] = e },
	} {
		changed := config
		changed.Docker = map[string]managedDockerConfig{key: config.Docker[key]}
		mutate(&changed)
		write(changed)
		if _, close, err := managedRuntimes(); err == nil {
			close()
			t.Fatal("unqualified managed configuration accepted")
		}
	}
	config.DefaultProvider = ""
	write(config)
	result, close, err = managedRuntimes()
	if err != nil {
		t.Fatal(err)
	}
	defer close()
	if result.DefaultProvider != "" || len(result.Providers) != 1 {
		t.Fatal("cleanup-only provider configuration lost")
	}
}
