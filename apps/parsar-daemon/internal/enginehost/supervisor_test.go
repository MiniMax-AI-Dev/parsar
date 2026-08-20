package enginehost

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The tests launch this same test binary as the "engine server": with
// enginehostFakeServerEnv set it serves /ping on the given port and does
// nothing else. That exercises the real process, port and readiness paths
// instead of stubbing them out, which is where the ownership bugs live.
const (
	enginehostFakeServerEnv = "ENGINEHOST_TEST_FAKE_SERVER_PORT"
	enginehostFakeDelayEnv  = "ENGINEHOST_TEST_FAKE_BOOT_DELAY"
	enginehostFakeFailEnv   = "ENGINEHOST_TEST_FAKE_FAIL"
)

func TestMain(m *testing.M) {
	if port := os.Getenv(enginehostFakeServerEnv); port != "" {
		runFakeServer(port)
		return
	}
	os.Exit(m.Run())
}

func runFakeServer(port string) {
	if os.Getenv(enginehostFakeFailEnv) != "" {
		fmt.Fprintln(os.Stderr, "fake engine: refusing to boot")
		os.Exit(3)
	}
	if delay := os.Getenv(enginehostFakeDelayEnv); delay != "" {
		if d, err := time.ParseDuration(delay); err == nil {
			time.Sleep(d)
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/ping", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	// Announced on stdout so a test can assert the tail is captured.
	fmt.Println("fake engine listening on " + port)
	srv := &http.Server{Addr: net.JoinHostPort(LoopbackHost, port), Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	_ = srv.ListenAndServe()
	os.Exit(0)
}

func fakeSpec(t *testing.T, key string, extraEnv ...string) ServerSpec {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test binary: %v", err)
	}
	return ServerSpec{
		Key:    key,
		Binary: exe,
		Env: func(port int) []string {
			env := append(os.Environ(), enginehostFakeServerEnv+"="+strconv.Itoa(port))
			return append(env, extraEnv...)
		},
		Ready:        pingReady,
		ReadyTimeout: 20 * time.Second,
		IdleTimeout:  time.Hour,
		KillTimeout:  time.Second,
		Logger:       testLogger(),
	}
}

func pingReady(ctx context.Context, baseURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/ping", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

func TestAcquireReusesOneServerPerKey(t *testing.T) {
	sup := NewSupervisor(testLogger())
	t.Cleanup(sup.Shutdown)

	first, err := sup.Acquire(context.Background(), fakeSpec(t, "reuse"))
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	second, err := sup.Acquire(context.Background(), fakeSpec(t, "reuse"))
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	if first.BaseURL() != second.BaseURL() {
		t.Fatalf("expected one shared server, got %q and %q", first.BaseURL(), second.BaseURL())
	}

	other, err := sup.Acquire(context.Background(), fakeSpec(t, "other-key"))
	if err != nil {
		t.Fatalf("other acquire: %v", err)
	}
	if other.BaseURL() == first.BaseURL() {
		t.Fatalf("distinct keys must not share a server, both got %q", other.BaseURL())
	}
	first.Release()
	second.Release()
	other.Release()
}

func TestAcquireDeduplicatesConcurrentLaunches(t *testing.T) {
	sup := NewSupervisor(testLogger())
	t.Cleanup(sup.Shutdown)

	// A boot delay widens the window in which a duplicate launch would
	// happen if the pending entry were not published before the launch.
	spec := fakeSpec(t, "concurrent", enginehostFakeDelayEnv+"=300ms")

	const callers = 6
	var wg sync.WaitGroup
	urls := make([]string, callers)
	errs := make([]error, callers)
	leases := make([]*Lease, callers)
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lease, err := sup.Acquire(context.Background(), spec)
			errs[i] = err
			if err == nil {
				leases[i] = lease
				urls[i] = lease.BaseURL()
			}
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
	}
	for i, u := range urls {
		if u != urls[0] {
			t.Fatalf("caller %d got %q, want the shared %q", i, u, urls[0])
		}
	}
	for _, l := range leases {
		l.Release()
	}
}

func TestReleaseWithNegativeIdleTimeoutStopsImmediately(t *testing.T) {
	sup := NewSupervisor(testLogger())
	t.Cleanup(sup.Shutdown)

	spec := fakeSpec(t, "eager-stop")
	spec.IdleTimeout = -1

	lease, err := sup.Acquire(context.Background(), spec)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	exited := lease.Exited()
	lease.Release()

	select {
	case <-exited:
	case <-time.After(10 * time.Second):
		t.Fatal("released instance was not stopped")
	}

	// A later Acquire must launch a replacement rather than hand back the
	// corpse.
	next, err := sup.Acquire(context.Background(), spec)
	if err != nil {
		t.Fatalf("re-acquire: %v", err)
	}
	if next.BaseURL() == "" {
		t.Fatal("replacement lease has no base URL")
	}
	next.Release()
}

func TestIdleReclamationStopsAbandonedServer(t *testing.T) {
	sup := NewSupervisor(testLogger())
	t.Cleanup(sup.Shutdown)

	spec := fakeSpec(t, "idle")
	spec.IdleTimeout = 150 * time.Millisecond

	lease, err := sup.Acquire(context.Background(), spec)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	exited := lease.Exited()
	lease.Release()

	select {
	case <-exited:
	case <-time.After(10 * time.Second):
		t.Fatal("idle instance was never reclaimed")
	}
}

func TestAcquireWithinIdleWindowKeepsServerWarm(t *testing.T) {
	sup := NewSupervisor(testLogger())
	t.Cleanup(sup.Shutdown)

	spec := fakeSpec(t, "warm")
	spec.IdleTimeout = 3 * time.Second

	first, err := sup.Acquire(context.Background(), spec)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	url := first.BaseURL()
	first.Release()

	second, err := sup.Acquire(context.Background(), spec)
	if err != nil {
		t.Fatalf("re-acquire: %v", err)
	}
	defer second.Release()
	if second.BaseURL() != url {
		t.Fatalf("expected the warm server %q, got %q", url, second.BaseURL())
	}
	select {
	case <-second.Exited():
		t.Fatal("warm server was stopped despite the new lease")
	default:
	}
}

func TestAcquireSurfacesBootFailureWithDiagnostics(t *testing.T) {
	sup := NewSupervisor(testLogger())
	t.Cleanup(sup.Shutdown)

	spec := fakeSpec(t, "bad-boot", enginehostFakeFailEnv+"=1")
	spec.ReadyTimeout = 10 * time.Second

	_, err := sup.Acquire(context.Background(), spec)
	if err == nil {
		t.Fatal("expected a boot failure")
	}
	if !strings.Contains(err.Error(), "refusing to boot") {
		t.Fatalf("error should carry the engine's own output, got %v", err)
	}

	// The failed entry must not be cached: the next attempt gets a real
	// launch, which for a now-healthy spec has to succeed.
	good := fakeSpec(t, "bad-boot")
	lease, err := sup.Acquire(context.Background(), good)
	if err != nil {
		t.Fatalf("retry after failure: %v", err)
	}
	lease.Release()
}

func TestAcquireReplacesDeadServer(t *testing.T) {
	sup := NewSupervisor(testLogger())
	t.Cleanup(sup.Shutdown)

	spec := fakeSpec(t, "dead")
	lease, err := sup.Acquire(context.Background(), spec)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	firstURL := lease.BaseURL()

	// Kill the engine out from under the live lease, the way a crash
	// would, and confirm the next Acquire launches a replacement instead
	// of handing out a dead instance.
	killLeaseProcess(t, sup, spec.Key)
	select {
	case <-lease.Exited():
	case <-time.After(10 * time.Second):
		t.Fatal("engine did not exit after kill")
	}

	next, err := sup.Acquire(context.Background(), spec)
	if err != nil {
		t.Fatalf("acquire after crash: %v", err)
	}
	defer next.Release()
	if next.BaseURL() == firstURL {
		t.Fatalf("expected a fresh server, still on %q", firstURL)
	}
	lease.Release()
}

func TestShutdownStopsEverything(t *testing.T) {
	sup := NewSupervisor(testLogger())

	a, err := sup.Acquire(context.Background(), fakeSpec(t, "shutdown-a"))
	if err != nil {
		t.Fatalf("acquire a: %v", err)
	}
	b, err := sup.Acquire(context.Background(), fakeSpec(t, "shutdown-b"))
	if err != nil {
		t.Fatalf("acquire b: %v", err)
	}

	sup.Shutdown()
	for name, ch := range map[string]<-chan struct{}{"a": a.Exited(), "b": b.Exited()} {
		select {
		case <-ch:
		case <-time.After(10 * time.Second):
			t.Fatalf("server %s survived shutdown", name)
		}
	}
	if _, err := sup.Acquire(context.Background(), fakeSpec(t, "shutdown-c")); err == nil {
		t.Fatal("Acquire must be refused after Shutdown")
	}
}

func TestFreeLoopbackPortReturnsBindablePorts(t *testing.T) {
	seen := map[int]bool{}
	for range 5 {
		port, err := freeLoopbackPort()
		if err != nil {
			t.Fatalf("freeLoopbackPort: %v", err)
		}
		if port <= 0 || port > 65535 {
			t.Fatalf("implausible port %d", port)
		}
		if seen[port] {
			t.Fatalf("port %d handed out twice in a row", port)
		}
		seen[port] = true
		l, err := net.Listen("tcp", net.JoinHostPort(LoopbackHost, strconv.Itoa(port)))
		if err != nil {
			t.Fatalf("reserved port %d is not bindable: %v", port, err)
		}
		_ = l.Close()
	}
}

func TestLineTailKeepsOnlyTheTail(t *testing.T) {
	tail := newLineTail(3)
	for i := range 6 {
		tail.push("line-" + strconv.Itoa(i))
	}
	got := tail.String()
	if got != "line-3 | line-4 | line-5" {
		t.Fatalf("unexpected tail %q", got)
	}
}

func TestZeroValueLeaseIsInert(t *testing.T) {
	var l *Lease
	l.Release() // must not panic
	if l.BaseURL() != "" {
		t.Fatal("nil lease should have no base URL")
	}
	select {
	case <-l.Exited():
	default:
		t.Fatal("nil lease should report exited")
	}
}

func TestSpecValidation(t *testing.T) {
	cases := map[string]ServerSpec{
		"missing key":    {Binary: "x", Ready: pingReady},
		"missing binary": {Key: "k", Ready: pingReady},
		"missing ready":  {Key: "k", Binary: "x"},
	}
	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			if err := spec.validate(); err == nil {
				t.Fatal("expected a validation error")
			}
		})
	}
}

// killLeaseProcess SIGKILLs the OS process behind the instance registered
// under key, simulating an engine crash.
func killLeaseProcess(t *testing.T, sup *Supervisor, key string) {
	t.Helper()
	sup.mu.Lock()
	e, ok := sup.entries[key]
	sup.mu.Unlock()
	if !ok || e.inst == nil || e.inst.proc.Cmd.Process == nil {
		t.Fatalf("no live instance for key %q", key)
	}
	if err := e.inst.proc.Cmd.Process.Kill(); err != nil {
		t.Fatalf("kill engine: %v", err)
	}
}

// testLogger keeps supervisor logging out of test output while still
// exercising every logging path.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))
}
