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

func TestDockerClientFromEnvOmitsUnsetLimits(t *testing.T) {
	c := dockerClientFromEnv(func(string) string { return "" }, "img", "", false)
	if c.Memory != "" || c.CPUs != "" || c.PidsLimit != "" {
		t.Fatalf("expected empty limits when env unset, got %+v", c)
	}
}
