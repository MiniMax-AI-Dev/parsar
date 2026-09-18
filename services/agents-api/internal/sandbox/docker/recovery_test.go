package docker

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/google/uuid"
	"github.com/moby/moby/client"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
)

// These controlled transport faults use real Docker compute and volumes. They
// establish Provider recovery observations, not native or model acceptance.
func TestDockerProviderRecoveryObservations(t *testing.T) {
	image := os.Getenv("AGENTS_RUNTIME_DOCKER_TEST_IMAGE")
	if image == "" {
		t.Skip("explicit Docker fixture image required")
	}
	seccomp, err := os.ReadFile("../../../deploy/codex/seccomp.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, fault := range []string{"lost-start-response", "partial-volumes"} {
		t.Run(fault, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
			defer cancel()
			transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", "/var/run/docker.sock")
			}}
			defer transport.CloseIdleConnections()
			var fired atomic.Bool
			var creates atomic.Int32
			proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/containers/create") {
					creates.Add(1)
					if fault == "partial-volumes" {
						fired.Store(true)
						http.Error(w, `{"message":"injected before container create"}`, 500)
						return
					}
				}
				request := r.Clone(r.Context())
				request.RequestURI = ""
				request.URL.Scheme = "http"
				request.URL.Host = "docker"
				request.Host = "docker"
				response, e := transport.RoundTrip(request)
				if e != nil {
					http.Error(w, "Docker transport failed", 502)
					return
				}
				defer response.Body.Close()
				if fault == "lost-start-response" && r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/start") && response.StatusCode == 204 && fired.CompareAndSwap(false, true) {
					connection, _, e := w.(http.Hijacker).Hijack()
					if e == nil {
						_ = connection.Close()
					}
					return
				}
				for k, values := range response.Header {
					for _, v := range values {
						w.Header().Add(k, v)
					}
				}
				w.WriteHeader(response.StatusCode)
				_, _ = io.Copy(w, response.Body)
			}))
			defer proxy.Close()
			c, e := client.New(client.WithHost(proxy.URL))
			if e != nil {
				t.Fatal(e)
			}
			defer c.Close()
			config := Config{InstallationID: uuid.NewString(), Image: image, Network: "bridge", Seccomp: string(seccomp)}
			p, e := New(c, config)
			if e != nil {
				t.Fatal(e)
			}
			b := sandbox.Bootstrap{Reference: sandbox.Reference{TenantID: uuid.NewString(), EnvironmentID: uuid.NewString(), AllocationID: uuid.NewString()}, SessionID: uuid.NewString(), DeviceID: uuid.NewString(), CoreURL: "http://core.invalid/api/v1", Credential: "synthetic-recovery-token"}
			direct, e := client.New(client.WithHost("unix:///var/run/docker.sock"))
			if e != nil {
				t.Fatal(e)
			}
			defer direct.Close()
			recovered, e := New(direct, config)
			if e != nil {
				t.Fatal(e)
			}
			defer func() {
				cleanup, stop := context.WithTimeout(context.Background(), 15*time.Second)
				defer stop()
				if e := recovered.Kill(cleanup, b.Reference); e != nil {
					t.Error(e)
				}
			}()
			if _, e := p.Create(ctx, b); e == nil || !fired.Load() {
				t.Fatal("Create did not expose the injected uncertainty")
			}
			info, e := recovered.GetInfo(ctx, b.Reference)
			if fault == "lost-start-response" {
				if e != nil || info.State != "running" || !info.BootstrapComplete {
					t.Fatalf("original allocation not recoverable: %+v %v", info, e)
				}
			} else {
				if !errors.Is(e, sandbox.ErrNotFound) {
					t.Fatalf("partial allocation observation: %v", e)
				}
				for _, suffix := range []string{"-home", "-environment"} {
					if _, e := direct.VolumeInspect(ctx, recovered.name(b.Reference)+suffix, client.VolumeInspectOptions{}); e != nil {
						t.Fatal("missing container concealed missing volume fixture", e)
					}
				}
			}
			if creates.Load() != 1 {
				t.Fatalf("Create was replayed %d times", creates.Load())
			}
			if e := recovered.Kill(ctx, b.Reference); e != nil {
				t.Fatal(e)
			}
			for _, suffix := range []string{"-home", "-environment"} {
				if _, e := direct.VolumeInspect(ctx, recovered.name(b.Reference)+suffix, client.VolumeInspectOptions{}); !errdefs.IsNotFound(e) {
					t.Fatal("owned volume remains", e)
				}
			}
		})
	}
}
