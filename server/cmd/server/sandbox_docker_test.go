package main

import "testing"

func TestDockerDialBackURLRewritesLoopback(t *testing.T) {
	cases := map[string]struct {
		wantURL     string
		wantGateway bool
	}{
		"http://127.0.0.1:18080":  {"http://host.docker.internal:18080", true},
		"http://localhost:18080":  {"http://host.docker.internal:18080", true},
		"http://[::1]:8080":       {"http://host.docker.internal:8080", true},
		"http://parsar-server:8080": {"http://parsar-server:8080", false},
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
