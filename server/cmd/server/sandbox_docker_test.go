package main

import "testing"

func TestDockerDialBackURLRewritesLoopback(t *testing.T) {
	cases := map[string]struct {
		wantURL     string
		wantGateway bool
	}{
		"http://127.0.0.1:18080":     {"http://host.docker.internal:18080", true},
		"http://localhost:18080":     {"http://host.docker.internal:18080", true},
		"http://[::1]:8080":          {"http://host.docker.internal:8080", true},
		"http://parsar-server:8080":  {"http://parsar-server:8080", false},
		"https://public.example.com": {"https://public.example.com", false},
	}
	for in, want := range cases {
		gotURL, gotGateway := dockerDialBackURL(in)
		if gotURL != want.wantURL || gotGateway != want.wantGateway {
			t.Fatalf("dockerDialBackURL(%q) = (%q, %v), want (%q, %v)",
				in, gotURL, gotGateway, want.wantURL, want.wantGateway)
		}
	}
}

func TestDockerClientFromEnvReadsResourceLimits(t *testing.T) {
	env := func(k string) string {
		return map[string]string{
			"AGENT_DAEMON_SANDBOX_DOCKER_MEMORY":     "2g",
			"AGENT_DAEMON_SANDBOX_DOCKER_CPUS":       "1.5",
			"AGENT_DAEMON_SANDBOX_DOCKER_PIDS_LIMIT": "512",
		}[k]
	}
	c := dockerClientFromEnv(env, "img", "net", true)
	if c.Image != "img" || c.Network != "net" || !c.HostGateway {
		t.Fatalf("base fields not wired: %+v", c)
	}
	if c.Memory != "2g" || c.CPUs != "1.5" || c.PidsLimit != "512" {
		t.Fatalf("resource limits not wired: %+v", c)
	}
}

func TestDockerClientFromEnvAppliesBuiltInDefaults(t *testing.T) {
	// With the env unset the operator still gets a safe built-in cap (2 CPU /
	// 4GB) so one runaway sandbox can't starve the host. PidsLimit stays unset:
	// a low pids cap is a classic build-breaker (make -j, go test ./...).
	c := dockerClientFromEnv(func(string) string { return "" }, "img", "", false)
	if c.CPUs != "2" || c.Memory != "4g" {
		t.Fatalf("expected default 2 CPU / 4g, got cpus=%q memory=%q", c.CPUs, c.Memory)
	}
	if c.PidsLimit != "" {
		t.Fatalf("expected pids-limit unset by default, got %q", c.PidsLimit)
	}
}

func TestDockerClientFromEnvEscapeHatchDisablesDefault(t *testing.T) {
	// An operator who wants docker's unbounded default back sets the env to
	// 0/unlimited/none (case-insensitive, trimmed); the flag is then omitted
	// rather than falling back to the built-in cap.
	for _, off := range []string{"0", "unlimited", "none", "UNLIMITED", " None "} {
		env := func(k string) string {
			switch k {
			case "AGENT_DAEMON_SANDBOX_DOCKER_MEMORY", "AGENT_DAEMON_SANDBOX_DOCKER_CPUS":
				return off
			}
			return ""
		}
		c := dockerClientFromEnv(env, "img", "", false)
		if c.Memory != "" || c.CPUs != "" {
			t.Fatalf("escape hatch %q: expected limits omitted, got cpus=%q memory=%q", off, c.CPUs, c.Memory)
		}
	}
}
