package agentdaemon

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/agentdaemon/binding"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/sandbox/e2b"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// fakeWSConn is a minimal gateway.WSConn implementation for tests
// that only need to construct a *gateway.Session so they can call
// reg.Register(sess). The session goroutines are not started, so the
// conn methods are never invoked at runtime — we just need the type
// to satisfy the interface contract.
type fakeWSConn struct{}

func newFakeWSConn() *fakeWSConn                      { return &fakeWSConn{} }
func (*fakeWSConn) ReadMessage() (int, []byte, error) { return 0, nil, nil }
func (*fakeWSConn) WriteMessage(int, []byte) error    { return nil }
func (*fakeWSConn) SetReadLimit(int64)                {}
func (*fakeWSConn) SetReadDeadline(time.Time) error   { return nil }
func (*fakeWSConn) SetWriteDeadline(time.Time) error  { return nil }
func (*fakeWSConn) Close() error                      { return nil }

// fakeE2BClient is a scriptable e2b.Client substitute for the
// provider tests. Records every call so assertions can verify command
// shape and timeout values.
type fakeE2BClient struct {
	mu sync.Mutex

	createCalls  int
	killCalls    int
	renewCalls   int
	runCommands  []string
	runEnvs      []map[string]string
	sandboxIDSeq int

	createErr  error
	killErr    error
	renewErr   error
	getInfoErr error
	runErrs    map[string]error // command-substring -> err

	// getInfoEndAt is what GetInfo returns when no error is set.
	// Tests that care about the live expires_at populate this; tests
	// that don't care leave it zero (the provider treats zero as
	// "unknown" and the SandboxStatus / Renew flows degrade cleanly).
	getInfoEndAt time.Time

	// getInfoCalls counts GetInfo invocations so tests can assert
	// the SandboxStatus / Renew code path actually queries e2b for
	// the live TTL.
	getInfoCalls int

	// onCreate fires after a successful Create and before returning.
	// Lets tests simulate "daemon dials in" by calling
	// registry.Register on a synthetic session.
	onCreate func(sb e2b.Sandbox)
	// onConnect fires when RunCommand sees "parsar-daemon connect".
	onConnect func(sb e2b.Sandbox)
}

func newFakeE2BClient() *fakeE2BClient {
	return &fakeE2BClient{runErrs: map[string]error{}}
}

func (f *fakeE2BClient) Create(_ context.Context, _ e2b.CreateInput) (e2b.Sandbox, error) {
	f.mu.Lock()
	f.createCalls++
	f.sandboxIDSeq++
	id := f.sandboxIDSeq
	err := f.createErr
	f.mu.Unlock()
	if err != nil {
		return e2b.Sandbox{}, err
	}
	sb := e2b.Sandbox{SandboxID: pad("sbx-", id), TemplateID: "parsar-sandbox-e2b"}
	if f.onCreate != nil {
		f.onCreate(sb)
	}
	return sb, nil
}

func (f *fakeE2BClient) Kill(_ context.Context, _ string) error {
	f.mu.Lock()
	f.killCalls++
	err := f.killErr
	f.mu.Unlock()
	return err
}

func (f *fakeE2BClient) Renew(_ context.Context, _ string, _ int) error {
	f.mu.Lock()
	f.renewCalls++
	err := f.renewErr
	f.mu.Unlock()
	return err
}

func (f *fakeE2BClient) GetInfo(_ context.Context, sandboxID string) (e2b.SandboxRuntimeInfo, error) {
	f.mu.Lock()
	f.getInfoCalls++
	endAt := f.getInfoEndAt
	err := f.getInfoErr
	f.mu.Unlock()
	if err != nil {
		return e2b.SandboxRuntimeInfo{}, err
	}
	return e2b.SandboxRuntimeInfo{SandboxID: sandboxID, EndAt: endAt, State: "running"}, nil
}

func (f *fakeE2BClient) RunCommand(_ context.Context, in e2b.RunCommandInput) (e2b.CommandResult, error) {
	f.mu.Lock()
	f.runCommands = append(f.runCommands, in.Command)
	envCopy := map[string]string{}
	for k, v := range in.Env {
		envCopy[k] = v
	}
	f.runEnvs = append(f.runEnvs, envCopy)
	for sub, err := range f.runErrs {
		if strings.Contains(in.Command, sub) {
			f.mu.Unlock()
			return e2b.CommandResult{Exited: true, Status: "1", Stderr: err.Error()}, nil
		}
	}
	f.mu.Unlock()
	if strings.Contains(in.Command, "parsar-daemon connect") && f.onConnect != nil {
		f.onConnect(in.Sandbox)
	}
	return e2b.CommandResult{Exited: true, Status: "0"}, nil
}

func pad(prefix string, n int) string {
	if n < 10 {
		return prefix + "0" + string(rune('0'+n))
	}
	return prefix + string(rune('0'+n/10)) + string(rune('0'+n%10))
}

// fakeMinter is a RuntimeMinter substitute that hands out predictable
// runtime IDs and tokens so tests can assert against them.
type fakeMinter struct {
	mu        sync.Mutex
	calls     int
	lastInput store.CreateRuntimePairingInput
	createFn  func(input store.CreateRuntimePairingInput) (store.CreateRuntimePairingResult, error)
	idSeq     int
	createErr error
}

func (m *fakeMinter) CreateRuntimePairing(_ context.Context, in store.CreateRuntimePairingInput) (store.CreateRuntimePairingResult, error) {
	m.mu.Lock()
	m.calls++
	m.lastInput = in
	if m.createErr != nil {
		err := m.createErr
		m.mu.Unlock()
		return store.CreateRuntimePairingResult{}, err
	}
	if m.createFn != nil {
		fn := m.createFn
		m.mu.Unlock()
		return fn(in)
	}
	m.idSeq++
	runtimeID := "dev-runtime-" + pad("", m.idSeq)
	m.mu.Unlock()
	return store.CreateRuntimePairingResult{
		Runtime:      store.RuntimeRead{ID: runtimeID, Type: "agent_daemon", Name: in.Name},
		PairingToken: "tok-" + runtimeID,
	}, nil
}

func (m *fakeMinter) SoftDeleteRuntimeByWorkspaceName(_ context.Context, _, _ string) error {
	return nil
}

// newTestRegistryWithDevice returns a real registry pre-populated
// with a session for deviceID. Used by tests that want to assert
// fast-path behaviour without firing the full cold-start flow.
func newTestRegistryWithDevice(t *testing.T, deviceID string) (*gateway.Registry, *gateway.Session) {
	t.Helper()
	reg := gateway.NewRegistry()
	conn := newFakeWSConn()
	sess := gateway.NewSession(conn, deviceID, "wks-1", "0.0.0", reg, nil)
	reg.Register(sess)
	return reg, sess
}

// TestNoopSandboxProvider: the always-failing fallback wired when
// e2b config is absent must return ErrSandboxProviderDisabled so the
// connector turns it into a clean user-facing error.
func TestNoopSandboxProvider(t *testing.T) {
	p := NoopSandboxProvider{}
	_, err := p.Acquire(context.Background(), connector.PromptInput{AgentID: "pa-1"})
	if !errors.Is(err, ErrSandboxProviderDisabled) {
		t.Fatalf("expected ErrSandboxProviderDisabled, got %v", err)
	}
	if err := p.Release(context.Background(), "pa-1"); err != nil {
		t.Fatalf("Release should be no-op: %v", err)
	}
	if n, err := p.Reap(context.Background()); err != nil || n != 0 {
		t.Fatalf("Reap should return (0, nil); got (%d, %v)", n, err)
	}
}

// TestNewE2BSandboxProvider_RequiredFields: each required field
// missing must yield a specific error message so a misconfigured
// deployment fails loudly at boot.
func TestNewE2BSandboxProvider_RequiredFields(t *testing.T) {
	base := E2BProviderConfig{
		Client:    newFakeE2BClient(),
		Store:     &fakeMinter{},
		Registry:  gateway.NewRegistry(),
		Binder:    binding.NewInMemoryBinder(),
		Template:  "parsar-sandbox-e2b",
		ServerURL: "https://parsar.example.com",
	}
	good, err := NewE2BSandboxProvider(base)
	if err != nil || good == nil {
		t.Fatalf("baseline config should succeed: %v", err)
	}

	cases := map[string]func(c *E2BProviderConfig){
		"client":     func(c *E2BProviderConfig) { c.Client = nil },
		"store":      func(c *E2BProviderConfig) { c.Store = nil },
		"registry":   func(c *E2BProviderConfig) { c.Registry = nil },
		"binder":     func(c *E2BProviderConfig) { c.Binder = nil },
		"template":   func(c *E2BProviderConfig) { c.Template = "" },
		"server_url": func(c *E2BProviderConfig) { c.ServerURL = "" },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			c := base
			mut(&c)
			if _, err := NewE2BSandboxProvider(c); err == nil {
				t.Fatalf("expected error when %s missing", name)
			}
		})
	}
}

// TestE2BSandboxProvider_AcquireColdStart: happy-path cold start.
// Mint → Create → Login → Connect → WaitForDevice → return deviceID.
// Verifies the call order via the fake's recorded command list.
func TestE2BSandboxProvider_AcquireColdStart(t *testing.T) {
	reg := gateway.NewRegistry()
	minter := &fakeMinter{}
	e2bClient := newFakeE2BClient()

	// Simulate the daemon dialling back when RunCommand("parsar-daemon
	// connect") finishes — register a fake session for the runtime
	// ID. We do this from the onConnect hook so it lands after the
	// connect command but before WaitForDevice's timeout.
	e2bClient.onConnect = func(_ e2b.Sandbox) {
		// The runtime ID minter handed out is "dev-runtime-01"; the
		// fakeMinter only mints once per test, so we can hard-code
		// the lookup. Real-life ordering is "login mints/persists,
		// then connect dials WS".
		go func() {
			// Tiny delay simulates the WS dial.
			time.Sleep(5 * time.Millisecond)
			conn := newFakeWSConn()
			sess := gateway.NewSession(conn, "dev-runtime-01", "wks-1", "0.0.0", reg, nil)
			reg.Register(sess)
		}()
	}

	p, err := NewE2BSandboxProvider(E2BProviderConfig{
		Client:    e2bClient,
		Store:     minter,
		Registry:  reg,
		Binder:    binding.NewInMemoryBinder(),
		Template:  "parsar-sandbox-e2b",
		ServerURL: "https://parsar.example.com",
	})
	if err != nil {
		t.Fatalf("provider construction: %v", err)
	}

	deviceID, err := p.Acquire(context.Background(), connector.PromptInput{
		AgentID:     "pa-1",
		WorkspaceID: "wks-1",
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if deviceID != "dev-runtime-01" {
		t.Fatalf("deviceID mismatch: got %q", deviceID)
	}
	if e2bClient.createCalls != 1 {
		t.Fatalf("expected 1 e2b Create, got %d", e2bClient.createCalls)
	}
	// Two RunCommand calls now: (1) seed platform config — writes the
	// Claude settings.json that points at the hook scripts; (2) the
	// parsar-daemon connect that actually pairs the daemon. Order matters:
	// the agent CLI inside the sandbox reads settings.json on boot, so
	// it must be on disk before the daemon (and therefore the CLI) is
	// allowed to start.
	if len(e2bClient.runCommands) != 2 {
		t.Fatalf("expected 2 RunCommand calls (seed + connect), got %d: %v",
			len(e2bClient.runCommands), e2bClient.runCommands)
	}
	seedCmd := e2bClient.runCommands[0]
	if !strings.Contains(seedCmd, claudeSettingsPath) || !strings.Contains(seedCmd, "base64 -d") {
		t.Fatalf("first RunCommand should seed Claude settings.json via base64; got %q", seedCmd)
	}
	cmd := e2bClient.runCommands[1]
	if !strings.Contains(cmd, "parsar-daemon connect") {
		t.Fatalf("second command should be connect, got %q", cmd)
	}
	for _, want := range []string{"--device-name dev-runtime-01", " -b"} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("connect command missing %q: %q", want, cmd)
		}
	}
	if strings.Contains(cmd, "tok-dev-runtime-01") || strings.Contains(cmd, "--token") || strings.Contains(cmd, "https://parsar.example.com") {
		t.Fatalf("connect command must not expose token or server URL: %q", cmd)
	}
	connectEnv := e2bClient.runEnvs[1]
	if got := connectEnv["PARSAR_DAEMON_CONNECT_URL"]; got != "https://parsar.example.com" {
		t.Fatalf("env URL = %q", got)
	}
	if got := connectEnv["PARSAR_DAEMON_CONNECT_TOKEN"]; got != "tok-dev-runtime-01" {
		t.Fatalf("env token = %q", got)
	}
	if minter.lastInput.WorkspaceID != "wks-1" {
		t.Fatalf("mint should pass workspaceID; got %q", minter.lastInput.WorkspaceID)
	}
}

// A warm Acquire only verifies and touches the runtime. Renewal is driven by
// the scheduled lifecycle scan, even when AutoRenew is enabled.
func TestE2BSandboxProvider_AcquireWarmCacheHit(t *testing.T) {
	reg, _ := newTestRegistryWithDevice(t, "dev-runtime-01")
	e2bClient := newFakeE2BClient()
	p, _ := NewE2BSandboxProvider(E2BProviderConfig{
		Client:    e2bClient,
		Store:     &fakeMinter{},
		Registry:  reg,
		Binder:    binding.NewInMemoryBinder(),
		Template:  "parsar-sandbox-e2b",
		ServerURL: "https://parsar.example.com",
		AutoRenew: true,
	})
	// Pre-seed cache as if a prior cold start had populated it.
	p.cache["pa-1"] = &sandboxEntry{
		deviceID: "dev-runtime-01",
		sandbox:  e2b.Sandbox{SandboxID: "sbx-warm"},
		lastUsed: time.Now().Add(-1 * time.Minute),
	}

	deviceID, err := p.Acquire(context.Background(), connector.PromptInput{
		AgentID: "pa-1", WorkspaceID: "wks-1",
	})
	if err != nil {
		t.Fatalf("Acquire (warm): %v", err)
	}
	if deviceID != "dev-runtime-01" {
		t.Fatalf("warm Acquire returned wrong deviceID: %q", deviceID)
	}
	if e2bClient.createCalls != 0 {
		t.Fatalf("warm cache hit should NOT call e2b Create; saw %d", e2bClient.createCalls)
	}
	if e2bClient.renewCalls != 0 {
		t.Fatalf("warm cache hit should not renew directly; saw %d calls", e2bClient.renewCalls)
	}
}

func TestE2BSandboxProvider_AcquireWarmCacheHitDoesNotRenewByDefault(t *testing.T) {
	reg, _ := newTestRegistryWithDevice(t, "dev-runtime-01")
	e2bClient := newFakeE2BClient()
	p, _ := NewE2BSandboxProvider(E2BProviderConfig{
		Client:    e2bClient,
		Store:     &fakeMinter{},
		Registry:  reg,
		Binder:    binding.NewInMemoryBinder(),
		Template:  "parsar-sandbox-e2b",
		ServerURL: "https://parsar.example.com",
	})
	p.cache["pa-1"] = &sandboxEntry{
		deviceID: "dev-runtime-01",
		sandbox:  e2b.Sandbox{SandboxID: "sbx-warm"},
		lastUsed: time.Now().Add(-time.Minute),
	}

	if _, err := p.Acquire(context.Background(), connector.PromptInput{
		AgentID: "pa-1", WorkspaceID: "wks-1",
	}); err != nil {
		t.Fatalf("Acquire (warm): %v", err)
	}
	if e2bClient.renewCalls != 0 {
		t.Fatalf("warm cache hit renewed without AutoRenew enabled; saw %d calls", e2bClient.renewCalls)
	}
}

func TestE2BSandboxProvider_AcquireWarmCacheHitSyncsLeasePolicy(t *testing.T) {
	reg, _ := newTestRegistryWithDevice(t, "dev-runtime-01")
	bindings := newFakeBindings()
	p, _ := NewE2BSandboxProvider(E2BProviderConfig{
		Client: newFakeE2BClient(), Store: &fakeMinter{}, Registry: reg,
		Binder: binding.NewInMemoryBinder(), Bindings: bindings,
		Template: "parsar-sandbox-e2b", ServerURL: "https://parsar.example.com",
	})
	p.cache["pa-1"] = &sandboxEntry{
		deviceID: "dev-runtime-01", sandbox: e2b.Sandbox{SandboxID: "sbx-warm"},
		bindingID: "binding-1", lastUsed: time.Now(), timeout: time.Hour,
		expiresAt: time.Now().Add(30 * time.Minute), autoRenew: true,
	}

	if _, err := p.Acquire(context.Background(), connector.PromptInput{
		AgentID: "pa-1", WorkspaceID: "wks-1",
		AgentConfig: map[string]any{"sandbox_ttl": "30m", "sandbox_auto_renew": false},
	}); err != nil {
		t.Fatalf("Acquire (warm): %v", err)
	}
	bindings.mu.Lock()
	leaseCalls := append([]leaseConfigCall(nil), bindings.leaseConfigCalls...)
	bindings.mu.Unlock()
	if len(leaseCalls) != 1 || leaseCalls[0].timeoutSeconds != 1800 || leaseCalls[0].thresholdSeconds != 0 {
		t.Fatalf("lease policy calls = %+v, want one 1800s/disabled call", leaseCalls)
	}
	if got := p.cache["pa-1"]; got.timeout != 30*time.Minute || got.autoRenew {
		t.Fatalf("cached policy = timeout %s autoRenew %v", got.timeout, got.autoRenew)
	}
}

func TestE2BSandboxProvider_AcquireWarmCacheRetriesLeasePolicySync(t *testing.T) {
	reg, _ := newTestRegistryWithDevice(t, "dev-runtime-01")
	bindings := newFakeBindings()
	bindings.leaseConfigErr = errors.New("database unavailable")
	p, _ := NewE2BSandboxProvider(E2BProviderConfig{
		Client: newFakeE2BClient(), Store: &fakeMinter{}, Registry: reg,
		Binder: binding.NewInMemoryBinder(), Bindings: bindings,
		Template: "parsar-sandbox-e2b", ServerURL: "https://parsar.example.com",
	})
	p.cache["pa-1"] = &sandboxEntry{
		deviceID: "dev-runtime-01", sandbox: e2b.Sandbox{SandboxID: "sbx-warm"},
		bindingID: "binding-1", lastUsed: time.Now(), timeout: time.Hour,
		expiresAt: time.Now().Add(time.Hour), autoRenew: true,
	}
	in := connector.PromptInput{
		AgentID: "pa-1", WorkspaceID: "wks-1",
		AgentConfig: map[string]any{"sandbox_ttl": "30m", "sandbox_auto_renew": false},
	}
	if _, err := p.Acquire(context.Background(), in); err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	if got := p.cache["pa-1"]; got.timeout != time.Hour || !got.autoRenew {
		t.Fatalf("failed DB sync must keep old cached policy: timeout=%s autoRenew=%v", got.timeout, got.autoRenew)
	}
	bindings.mu.Lock()
	bindings.leaseConfigErr = nil
	bindings.mu.Unlock()
	if _, err := p.Acquire(context.Background(), in); err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	if got := p.cache["pa-1"]; got.timeout != 30*time.Minute || got.autoRenew {
		t.Fatalf("retried policy was not committed: timeout=%s autoRenew=%v", got.timeout, got.autoRenew)
	}
	if len(bindings.leaseConfigCalls) != 2 {
		t.Fatalf("lease sync calls=%d, want 2", len(bindings.leaseConfigCalls))
	}
}

func TestNormalizeSandboxTTL(t *testing.T) {
	for _, tt := range []struct {
		input time.Duration
		want  time.Duration
		ok    bool
	}{
		{input: 500 * time.Millisecond},
		{input: 1500 * time.Millisecond, want: time.Second, ok: true},
		{input: time.Hour, want: time.Hour, ok: true},
		{input: time.Duration(maxSandboxTTLSeconds)*time.Second + time.Second},
	} {
		got, ok := NormalizeSandboxTTL(tt.input)
		if got != tt.want || ok != tt.ok {
			t.Fatalf("NormalizeSandboxTTL(%s) = %s, %v; want %s, %v", tt.input, got, ok, tt.want, tt.ok)
		}
	}
}

func TestAutoRenewThresholdRequiresLongerThanMaintenanceInterval(t *testing.T) {
	if got := autoRenewThreshold(SandboxMaintenanceInterval, true); got != 0 {
		t.Fatalf("threshold at scan interval = %d, want disabled", got)
	}
	if got := autoRenewThreshold(30*time.Minute, true); got != 450 {
		t.Fatalf("threshold for 30m TTL = %d, want 450", got)
	}
}

// TestE2BSandboxProvider_AcquireRecoversFromDeadDevice: cached entry
// but the device is gone from the registry (daemon crashed). The
// provider must evict the dead entry and cold-start fresh.
func TestE2BSandboxProvider_AcquireRecoversFromDeadDevice(t *testing.T) {
	reg := gateway.NewRegistry() // empty: no device registered
	minter := &fakeMinter{}
	e2bClient := newFakeE2BClient()
	e2bClient.onConnect = func(_ e2b.Sandbox) {
		go func() {
			time.Sleep(5 * time.Millisecond)
			conn := newFakeWSConn()
			sess := gateway.NewSession(conn, "dev-runtime-01", "wks-1", "0.0.0", reg, nil)
			reg.Register(sess)
		}()
	}
	p, _ := NewE2BSandboxProvider(E2BProviderConfig{
		Client: e2bClient, Store: minter, Registry: reg,
		Binder:   binding.NewInMemoryBinder(),
		Template: "parsar-sandbox-e2b", ServerURL: "https://parsar.example.com",
	})
	// Pre-seed with a stale entry whose device is not in the registry.
	p.cache["pa-1"] = &sandboxEntry{
		deviceID: "dev-stale-99",
		sandbox:  e2b.Sandbox{SandboxID: "sbx-stale"},
		lastUsed: time.Now(),
	}

	deviceID, err := p.Acquire(context.Background(), connector.PromptInput{
		AgentID: "pa-1", WorkspaceID: "wks-1",
	})
	if err != nil {
		t.Fatalf("Acquire after dead device: %v", err)
	}
	if deviceID == "dev-stale-99" {
		t.Fatalf("provider returned stale dead deviceID")
	}
	if e2bClient.createCalls != 1 {
		t.Fatalf("dead cache should trigger fresh Create; saw %d", e2bClient.createCalls)
	}
	if e2bClient.killCalls < 1 {
		t.Fatalf("dead cache should kill stale sandbox; saw %d", e2bClient.killCalls)
	}
}

// TestE2BSandboxProvider_AcquireSerialisesConcurrent: 5 concurrent
// Acquires for the same agent must result in exactly one
// e2b.Create call. The other 4 wait on the inflight promise.
func TestE2BSandboxProvider_AcquireSerialisesConcurrent(t *testing.T) {
	reg := gateway.NewRegistry()
	minter := &fakeMinter{}
	e2bClient := newFakeE2BClient()
	e2bClient.onConnect = func(_ e2b.Sandbox) {
		go func() {
			time.Sleep(20 * time.Millisecond)
			conn := newFakeWSConn()
			sess := gateway.NewSession(conn, "dev-runtime-01", "wks-1", "0.0.0", reg, nil)
			reg.Register(sess)
		}()
	}
	p, _ := NewE2BSandboxProvider(E2BProviderConfig{
		Client: e2bClient, Store: minter, Registry: reg,
		Binder:   binding.NewInMemoryBinder(),
		Template: "parsar-sandbox-e2b", ServerURL: "https://parsar.example.com",
	})

	var wg sync.WaitGroup
	results := make([]string, 5)
	errs := make([]error, 5)
	for i := range results {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx], errs[idx] = p.Acquire(context.Background(), connector.PromptInput{
				AgentID: "pa-1", WorkspaceID: "wks-1",
			})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
		if results[i] != "dev-runtime-01" {
			t.Fatalf("goroutine %d returned wrong deviceID: %q", i, results[i])
		}
	}
	if e2bClient.createCalls != 1 {
		t.Fatalf("concurrent Acquires must serialise to 1 Create; got %d", e2bClient.createCalls)
	}
}

// TestE2BSandboxProvider_AcquireConnectFailureKillsSandbox: if connect
// fails inside the sandbox, the half-built sandbox must be killed so
// e2b doesn't leak resources.
func TestE2BSandboxProvider_AcquireConnectFailureKillsSandbox(t *testing.T) {
	reg := gateway.NewRegistry()
	minter := &fakeMinter{}
	e2bClient := newFakeE2BClient()
	e2bClient.runErrs["parsar-daemon connect"] = errors.New("invalid token")

	p, _ := NewE2BSandboxProvider(E2BProviderConfig{
		Client: e2bClient, Store: minter, Registry: reg,
		Binder:   binding.NewInMemoryBinder(),
		Template: "parsar-sandbox-e2b", ServerURL: "https://parsar.example.com",
	})
	_, err := p.Acquire(context.Background(), connector.PromptInput{
		AgentID: "pa-1", WorkspaceID: "wks-1",
	})
	if err == nil {
		t.Fatalf("expected error on connect failure")
	}
	if !errors.Is(err, ErrSandboxAcquireFailed) {
		t.Fatalf("expected ErrSandboxAcquireFailed wrap, got %v", err)
	}
	if e2bClient.killCalls != 1 {
		t.Fatalf("failed connect should trigger Kill; saw %d", e2bClient.killCalls)
	}
}

// TestE2BSandboxProvider_AcquireWaitForDeviceTimeout: e2b Create +
// commands succeed but the daemon never dials back. Provider must
// kill the sandbox + return wrapped ErrWaitForDeviceTimeout.
func TestE2BSandboxProvider_AcquireWaitForDeviceTimeout(t *testing.T) {
	reg := gateway.NewRegistry()
	minter := &fakeMinter{}
	e2bClient := newFakeE2BClient()
	// onConnect intentionally nil — no daemon dials back, so
	// WaitForDevice will hit the timeout.

	// Lower the connect-side timeout so the test isn't slow.
	prevConn, prevAcquire := SandboxConnectTimeout, SandboxAcquireTimeout
	SandboxConnectTimeout = 100 * time.Millisecond
	SandboxAcquireTimeout = 1 * time.Second
	t.Cleanup(func() {
		SandboxConnectTimeout = prevConn
		SandboxAcquireTimeout = prevAcquire
	})

	p, _ := NewE2BSandboxProvider(E2BProviderConfig{
		Client: e2bClient, Store: minter, Registry: reg,
		Binder:   binding.NewInMemoryBinder(),
		Template: "parsar-sandbox-e2b", ServerURL: "https://parsar.example.com",
	})
	_, err := p.Acquire(context.Background(), connector.PromptInput{
		AgentID: "pa-1", WorkspaceID: "wks-1",
	})
	if err == nil {
		t.Fatalf("expected timeout error")
	}
	if !errors.Is(err, ErrSandboxAcquireFailed) {
		t.Fatalf("expected ErrSandboxAcquireFailed wrap, got %v", err)
	}
	if e2bClient.killCalls != 1 {
		t.Fatalf("timeout must Kill sandbox; saw %d kills", e2bClient.killCalls)
	}
}

// TestE2BSandboxProvider_Release: cached entry exists → Release kills
// the sandbox + drops the cache entry + invalidates the binder. A
// second Release is a no-op (idempotent).
func TestE2BSandboxProvider_Release(t *testing.T) {
	reg, _ := newTestRegistryWithDevice(t, "dev-runtime-01")
	e2bClient := newFakeE2BClient()
	binder := binding.NewInMemoryBinder()
	_ = binder.Bind(context.Background(), binding.Binding{
		ConversationID: "conv-1", AgentID: "pa-1", DeviceID: "dev-runtime-01",
	})
	p, _ := NewE2BSandboxProvider(E2BProviderConfig{
		Client: e2bClient, Store: &fakeMinter{}, Registry: reg, Binder: binder,
		Template: "parsar-sandbox-e2b", ServerURL: "https://parsar.example.com",
	})
	p.cache["pa-1"] = &sandboxEntry{
		deviceID: "dev-runtime-01",
		sandbox:  e2b.Sandbox{SandboxID: "sbx-warm"},
		lastUsed: time.Now(),
	}
	if err := p.Release(context.Background(), "pa-1"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if e2bClient.killCalls != 1 {
		t.Fatalf("Release should Kill; saw %d", e2bClient.killCalls)
	}
	if _, err := binder.Resolve(context.Background(), "conv-1", "pa-1", "claude_code"); !errors.Is(err, binding.ErrNotBound) {
		t.Fatalf("Release should invalidate binder; resolve returned %v", err)
	}
	// Second release: no-op.
	if err := p.Release(context.Background(), "pa-1"); err != nil {
		t.Fatalf("second Release should be no-op: %v", err)
	}
}

func TestE2BSandboxProvider_ReleaseUsesDurableBindingOnCacheMiss(t *testing.T) {
	e2bClient := newFakeE2BClient()
	bindings := newFakeBindings()
	bindings.activeFound = true
	bindings.activeBinding = store.SandboxBindingRead{
		ID: "binding-remote", SandboxID: "sbx-remote",
		Metadata: map[string]any{"device_id": "dev-remote"},
	}
	binder := binding.NewInMemoryBinder()
	_ = binder.Bind(context.Background(), binding.Binding{
		ConversationID: "conv-remote", AgentID: "pa-remote", DeviceID: "dev-remote",
	})
	p, _ := NewE2BSandboxProvider(E2BProviderConfig{
		Client: e2bClient, Store: &fakeMinter{}, Registry: gateway.NewRegistry(), Binder: binder,
		Bindings: bindings, Template: "parsar-sandbox-e2b", ServerURL: "https://parsar.example.com",
	})
	if err := p.Release(context.Background(), "pa-remote"); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if e2bClient.killCalls != 1 {
		t.Fatalf("cache-miss Release should kill durable sandbox; got %d calls", e2bClient.killCalls)
	}
	if len(bindings.markKilled) != 1 || bindings.markKilled[0] != "binding-remote" {
		t.Fatalf("marked bindings=%v, want [binding-remote]", bindings.markKilled)
	}
	if _, err := binder.Resolve(context.Background(), "conv-remote", "pa-remote", "claude_code"); !errors.Is(err, binding.ErrNotBound) {
		t.Fatalf("Release should invalidate remote binder entry; got %v", err)
	}
}

// TestE2BSandboxProvider_Reap: entries older than the threshold are
// evicted + killed; fresh entries survive.
func TestE2BSandboxProvider_Reap(t *testing.T) {
	reg := gateway.NewRegistry()
	e2bClient := newFakeE2BClient()
	p, _ := NewE2BSandboxProvider(E2BProviderConfig{
		Client: e2bClient, Store: &fakeMinter{}, Registry: reg,
		Binder:   binding.NewInMemoryBinder(),
		Template: "parsar-sandbox-e2b", ServerURL: "https://parsar.example.com",
	})
	prev := SandboxIdleReapThreshold
	SandboxIdleReapThreshold = 1 * time.Minute
	t.Cleanup(func() { SandboxIdleReapThreshold = prev })

	now := time.Now().UTC()
	p.cache["pa-stale"] = &sandboxEntry{
		deviceID: "dev-stale", sandbox: e2b.Sandbox{SandboxID: "sbx-stale"},
		lastUsed: now.Add(-5 * time.Minute),
	}
	p.cache["pa-fresh"] = &sandboxEntry{
		deviceID: "dev-fresh", sandbox: e2b.Sandbox{SandboxID: "sbx-fresh"},
		lastUsed: now.Add(-10 * time.Second),
	}
	p.cache["pa-auto-renew"] = &sandboxEntry{
		deviceID: "dev-auto-renew", sandbox: e2b.Sandbox{SandboxID: "sbx-auto-renew"},
		lastUsed: now.Add(-5 * time.Minute), autoRenew: true,
	}
	p.cache["pa-long-ttl"] = &sandboxEntry{
		deviceID: "dev-long-ttl", sandbox: e2b.Sandbox{SandboxID: "sbx-long-ttl"},
		lastUsed: now.Add(-5 * time.Minute), timeout: 10 * time.Minute,
	}

	n, err := p.Reap(context.Background())
	if err != nil {
		t.Fatalf("Reap: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 evicted, got %d", n)
	}
	if _, ok := p.cache["pa-stale"]; ok {
		t.Fatalf("stale entry should have been evicted")
	}
	if _, ok := p.cache["pa-fresh"]; !ok {
		t.Fatalf("fresh entry should survive")
	}
	if _, ok := p.cache["pa-auto-renew"]; !ok {
		t.Fatalf("auto-renew entry should survive idle reap")
	}
	if _, ok := p.cache["pa-long-ttl"]; !ok {
		t.Fatalf("entry-specific TTL should control idle reap")
	}
	if e2bClient.killCalls != 1 {
		t.Fatalf("Reap should Kill exactly one sandbox; got %d", e2bClient.killCalls)
	}
}

func TestE2BSandboxProvider_ReapHonorsLatestDurablePolicy(t *testing.T) {
	bindings := newFakeBindings()
	bindings.activeFound = true
	bindings.activeBinding = store.SandboxBindingRead{
		ID: "binding-1", SandboxID: "sbx-1",
		TimeoutSeconds: 3600, AutoRenewThresholdSeconds: 900,
		LastActiveAt: time.Now().UTC(),
	}
	client := newFakeE2BClient()
	p, _ := NewE2BSandboxProvider(E2BProviderConfig{
		Client: client, Store: &fakeMinter{}, Registry: gateway.NewRegistry(),
		Binder: binding.NewInMemoryBinder(), Bindings: bindings,
		Template: "parsar-sandbox-e2b", ServerURL: "https://parsar.example.com",
	})
	p.cache["pa-1"] = &sandboxEntry{
		deviceID: "dev-1", sandbox: e2b.Sandbox{SandboxID: "sbx-1"},
		lastUsed: time.Now().UTC().Add(-2 * time.Hour), timeout: time.Hour,
	}

	if n, err := p.Reap(context.Background()); err != nil || n != 0 {
		t.Fatalf("Reap = %d, %v; want 0, nil", n, err)
	}
	if _, ok := p.cache["pa-1"]; !ok {
		t.Fatal("durable auto-renew policy should restore the cache entry")
	}
	if client.killCalls != 0 {
		t.Fatalf("durable auto-renew sandbox should not be killed; got %d calls", client.killCalls)
	}
}

func TestE2BSandboxProvider_ReapRetriesProviderKillFailure(t *testing.T) {
	client := newFakeE2BClient()
	client.killErr = errors.New("provider unavailable")
	p, _ := NewE2BSandboxProvider(E2BProviderConfig{
		Client: client, Store: &fakeMinter{}, Registry: gateway.NewRegistry(),
		Binder: binding.NewInMemoryBinder(), Template: "parsar-sandbox-e2b",
		ServerURL: "https://parsar.example.com",
	})
	p.cache["pa-1"] = &sandboxEntry{
		deviceID: "dev-1", sandbox: e2b.Sandbox{SandboxID: "sbx-1"},
		lastUsed: time.Now().UTC().Add(-2 * time.Hour), timeout: time.Hour,
	}
	if n, err := p.Reap(context.Background()); err != nil || n != 0 {
		t.Fatalf("Reap = %d, %v; want 0, nil", n, err)
	}
	if _, ok := p.cache["pa-1"]; !ok {
		t.Fatal("failed provider kill should restore the cache entry for retry")
	}
}

func TestE2BSandboxProvider_SandboxExistsRequiresExplicitNotFound(t *testing.T) {
	client := newFakeE2BClient()
	p := &E2BSandboxProvider{
		cfg: E2BProviderConfig{Client: client},
		cache: map[string]*sandboxEntry{
			"pa-1": {sandbox: e2b.Sandbox{SandboxID: "sbx-1"}},
		},
	}

	exists, confirmed, err := p.SandboxExists(context.Background(), "pa-1")
	if err != nil || !exists || !confirmed {
		t.Fatalf("live probe = (%v, %v, %v), want (true, true, nil)", exists, confirmed, err)
	}
	client.getInfoErr = errors.New("provider unavailable")
	if _, _, err := p.SandboxExists(context.Background(), "pa-1"); err == nil {
		t.Fatal("transient provider failure must not confirm deletion")
	}
	client.getInfoErr = &e2b.HTTPError{StatusCode: http.StatusNotFound, Message: "missing"}
	exists, confirmed, err = p.SandboxExists(context.Background(), "pa-1")
	if err != nil || exists || !confirmed {
		t.Fatalf("404 probe = (%v, %v, %v), want (false, true, nil)", exists, confirmed, err)
	}
}

func TestE2BSandboxProvider_RecreateStopsWhenKillFails(t *testing.T) {
	client := newFakeE2BClient()
	client.killErr = errors.New("provider unavailable")
	p := &E2BSandboxProvider{
		cfg: E2BProviderConfig{Client: client, Binder: binding.NewInMemoryBinder()},
		cache: map[string]*sandboxEntry{
			"pa-1": {
				bindingID: "binding-1", deviceID: "dev-1",
				sandbox: e2b.Sandbox{SandboxID: "sbx-1"},
			},
		},
		inflight: map[string]*acquirePromise{},
	}

	if _, err := p.Recreate(context.Background(), connector.PromptInput{AgentID: "pa-1"}); err == nil {
		t.Fatal("Recreate should fail when the old sandbox cannot be killed")
	}
	if client.createCalls != 0 {
		t.Fatalf("replacement sandbox created after kill failure: %d", client.createCalls)
	}
	if _, ok := p.cache["pa-1"]; !ok {
		t.Fatal("kill failure should restore the cache entry")
	}
}

func TestE2BSandboxProvider_ReapDoesNotKillCrossPodReplacement(t *testing.T) {
	bindings := newFakeBindings()
	bindings.activeFound = true
	bindings.activeBinding = store.SandboxBindingRead{
		ID: "binding-new", SandboxID: "sbx-new", TimeoutSeconds: 3600,
	}
	client := newFakeE2BClient()
	p, _ := NewE2BSandboxProvider(E2BProviderConfig{
		Client: client, Store: &fakeMinter{}, Registry: gateway.NewRegistry(),
		Binder: binding.NewInMemoryBinder(), Bindings: bindings,
		Template: "parsar-sandbox-e2b", ServerURL: "https://parsar.example.com",
	})
	p.cache["pa-1"] = &sandboxEntry{
		deviceID: "dev-old", sandbox: e2b.Sandbox{SandboxID: "sbx-old"},
		bindingID: "binding-old", lastUsed: time.Now().UTC().Add(-2 * time.Hour), timeout: time.Hour,
	}
	if n, err := p.Reap(context.Background()); err != nil || n != 1 {
		t.Fatalf("Reap = %d, %v; want 1 stale cache eviction", n, err)
	}
	if client.killCalls != 0 || len(bindings.markKilled) != 0 {
		t.Fatalf("replacement must remain untouched: kills=%d marked=%v", client.killCalls, bindings.markKilled)
	}
}

// TestShellSingleQuote covers the bash-quoting helper. Three classes:
// safe alnum (no quoting), shell-metas (single-quote wrapped),
// embedded single quote (broken-out via '\”).
func TestShellSingleQuote(t *testing.T) {
	cases := map[string]string{
		"abc":         "abc",
		"dev-runtime": "dev-runtime",
		"http://x/y":  "http://x/y",
		"a b c":       "'a b c'",
		"foo'bar":     "'foo'\\''bar'",
		"":            "",
		"!@#":         "'!@#'",
	}
	for in, want := range cases {
		got := shellSingleQuote(in)
		if got != want {
			t.Errorf("shellSingleQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestE2BSandboxProvider_SeedFailureKillsSandbox: if the platform-
// config seed Exec fails (e.g. sandbox FS read-only because of a
// broken template), Acquire must kill the half-built sandbox AND must
// never attempt the parsar-daemon connect — emitting connect after a
// missing settings.json would silently produce a sandbox whose agent
// CLI runs without any spec/memory injection.
func TestE2BSandboxProvider_SeedFailureKillsSandbox(t *testing.T) {
	reg := gateway.NewRegistry()
	minter := &fakeMinter{}
	e2bClient := newFakeE2BClient()
	// Trigger failure on the seed RunCommand. "mkdir" is part of the
	// seed shell pipeline (writeRemoteFile's mkdir -p prefix); using
	// a substring that won't match "parsar-daemon connect" keeps the two
	// failure modes independent.
	e2bClient.runErrs["mkdir"] = errors.New("read-only filesystem")

	p, _ := NewE2BSandboxProvider(E2BProviderConfig{
		Client: e2bClient, Store: minter, Registry: reg,
		Binder:   binding.NewInMemoryBinder(),
		Template: "parsar-sandbox-e2b", ServerURL: "https://parsar.example.com",
	})
	_, err := p.Acquire(context.Background(), connector.PromptInput{
		AgentID: "pa-1", WorkspaceID: "wks-1",
	})
	if err == nil {
		t.Fatalf("expected error on seed failure")
	}
	if !errors.Is(err, ErrSandboxAcquireFailed) {
		t.Fatalf("expected ErrSandboxAcquireFailed wrap, got %v", err)
	}
	if !strings.Contains(err.Error(), "seed platform config") {
		t.Errorf("error should identify the seed step: %v", err)
	}
	if e2bClient.killCalls != 1 {
		t.Fatalf("seed failure must Kill the half-built sandbox; saw %d", e2bClient.killCalls)
	}
	// Critical invariant: no parsar-daemon connect after a failed seed.
	// Otherwise we'd boot a daemon into a sandbox with no settings.json
	// and never realise the agent CLI ran without spec/memory injection.
	for _, cmd := range e2bClient.runCommands {
		if strings.Contains(cmd, "parsar-daemon connect") {
			t.Fatalf("connect must NOT fire after seed failure; commands: %v", e2bClient.runCommands)
		}
	}
}

// TestE2BSandboxProvider_ColdStartPropagatesTGEnv: every PARSAR_* env var
// the agent CLI inside the sandbox reads must reach the daemon connect
// RunCommand call. This is the only path that gets them in — there is
// no other writer for the env block, so a missed key here means the
// hook scripts inside the sandbox would silently fail-open with empty
// additionalContext on every prompt. PromptInput fields source nearly
// every value; the test pins each one so a future refactor that drops
// a key surfaces immediately.
func TestE2BSandboxProvider_ColdStartPropagatesTGEnv(t *testing.T) {
	reg := gateway.NewRegistry()
	minter := &fakeMinter{}
	e2bClient := newFakeE2BClient()
	e2bClient.onConnect = func(_ e2b.Sandbox) {
		go func() {
			time.Sleep(5 * time.Millisecond)
			conn := newFakeWSConn()
			sess := gateway.NewSession(conn, "dev-runtime-01", "wks-1", "0.0.0", reg, nil)
			reg.Register(sess)
		}()
	}

	p, _ := NewE2BSandboxProvider(E2BProviderConfig{
		Client: e2bClient, Store: minter, Registry: reg,
		Binder:    binding.NewInMemoryBinder(),
		Template:  "parsar-sandbox-e2b",
		ServerURL: "https://parsar.example.com",
		// Leave Connector zero-value: documented to default to claude
		// because the only published template is Claude-based. Test
		// asserts PARSAR_CONNECTOR=claude below to lock that behaviour.
	})

	_, err := p.Acquire(context.Background(), connector.PromptInput{
		AgentID:                 "pa-1",
		WorkspaceID:             "wks-1",
		ConversationID:          "conv-42",
		ConversationInitiatorID: "user-99",
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	// runEnvs[0] is the seed RunCommand (no env block in current
	// implementation); runEnvs[1] is the parsar-daemon connect call.
	if len(e2bClient.runEnvs) != 2 {
		t.Fatalf("expected 2 RunCommand calls, got %d", len(e2bClient.runEnvs))
	}
	env := e2bClient.runEnvs[1]
	wantEnv := map[string]string{
		// Daemon-only keys: URL + pairing token.
		"PARSAR_DAEMON_CONNECT_URL":   "https://parsar.example.com",
		"PARSAR_DAEMON_CONNECT_TOKEN": "tok-dev-runtime-01",
		// CLI keys — same token, different name.
		"PARSAR_SERVER_URL":   "https://parsar.example.com",
		"PARSAR_RUNNER_TOKEN": "tok-dev-runtime-01",
		"PARSAR_RUNTIME_ID":   "dev-runtime-01",
		"PARSAR_WORKSPACE_ID": "wks-1",
		"PARSAR_AGENT_ID":     "pa-1",
		"PARSAR_CONNECTOR":    "claude",
		// Optional keys: present when PromptInput supplied them.
		"PARSAR_USER_ID":         "user-99",
		"PARSAR_CONVERSATION_ID": "conv-42",
	}
	for k, v := range wantEnv {
		if got, ok := env[k]; !ok {
			t.Errorf("missing env key %q (want %q); full env=%v", k, v, env)
		} else if got != v {
			t.Errorf("env[%q] = %q, want %q", k, got, v)
		}
	}
}

// TestE2BSandboxProvider_ColdStartOmitsEmptyTGEnv: optional PARSAR_* keys
// must be ABSENT (not set to "") when PromptInput leaves them empty.
// Hook scripts inside the sandbox use `os.environ.get("PARSAR_USER_ID")`
// as a presence signal; an empty string would still be truthy and break
// that check. This test guards the symmetry by supplying only the
// mandatory fields.
func TestE2BSandboxProvider_ColdStartOmitsEmptyTGEnv(t *testing.T) {
	reg := gateway.NewRegistry()
	minter := &fakeMinter{}
	e2bClient := newFakeE2BClient()
	e2bClient.onConnect = func(_ e2b.Sandbox) {
		go func() {
			time.Sleep(5 * time.Millisecond)
			conn := newFakeWSConn()
			sess := gateway.NewSession(conn, "dev-runtime-01", "wks-1", "0.0.0", reg, nil)
			reg.Register(sess)
		}()
	}

	p, _ := NewE2BSandboxProvider(E2BProviderConfig{
		Client: e2bClient, Store: minter, Registry: reg,
		Binder:    binding.NewInMemoryBinder(),
		Template:  "parsar-sandbox-e2b",
		ServerURL: "https://parsar.example.com",
		Connector: SandboxConnectorClaude, // explicit, not zero-value
	})

	// Only mandatory fields supplied. Optionals (ConversationID,
	// ConversationInitiatorID) intentionally empty.
	_, err := p.Acquire(context.Background(), connector.PromptInput{
		AgentID:     "pa-1",
		WorkspaceID: "wks-1",
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	env := e2bClient.runEnvs[1]
	for _, key := range []string{"PARSAR_USER_ID", "PARSAR_CONVERSATION_ID"} {
		if _, present := env[key]; present {
			t.Errorf("env[%q] should be absent when PromptInput field is empty; got %q", key, env[key])
		}
	}
	// Mandatory keys still present.
	for _, key := range []string{"PARSAR_RUNNER_TOKEN", "PARSAR_WORKSPACE_ID", "PARSAR_RUNTIME_ID", "PARSAR_CONNECTOR"} {
		if _, present := env[key]; !present {
			t.Errorf("env[%q] should always be present", key)
		}
	}
}

// ------------------------------------------------------------------
// Cross-pod coordination tests (ReserveSandboxBindingSlot path)
// ------------------------------------------------------------------

// fakeBindings is a SandboxBindingPersister substitute that lets
// individual tests choose whether the Reserve call wins, loses, or
// errors, and records every Finalize / MarkKilled invocation for
// post-hoc assertions. Concurrency-safe so tests can fire multiple
// Acquire goroutines at once.
type fakeBindings struct {
	mu sync.Mutex

	// reserveFn is what ReserveSandboxBindingSlot returns. nil means
	// always-win and seed a brand-new spawning row keyed by
	// agent_id; tests that want the loser path set this.
	reserveFn func(in store.ReserveSandboxBindingSlotInput) (store.SandboxBindingRead, bool, error)

	// waitFn is what WaitForSandboxBindingActive returns for losers.
	// Default: refuse to wait (return ErrSandboxBindingFailed) so
	// tests that don't opt into the loser path fail loudly if they
	// hit it by accident.
	waitFn func(workspaceID, agentID string) (store.SandboxBindingRead, error)

	// Recorded invocations — tests assert on these.
	createCalls      int
	reserveCalls     int
	finalizeCalls    int
	markKilled       []string // binding IDs
	waitCalls        int
	claims           []store.SandboxAutoRenewClaim
	completedRenew   []string
	failedRenew      []string
	reclaimCalls     int
	reclaimResult    bool
	reclaimErr       error
	leaseConfigCalls []leaseConfigCall
	leaseConfigErr   error
	activeBinding    store.SandboxBindingRead
	activeFound      bool
	activeErr        error

	// Latest Finalize input for content asserts.
	lastFinalize store.FinalizeSandboxBindingSpawningInput

	// Latest sandbox id recorded via SetSpawningSandboxBindingSandboxID,
	// keyed by binding id, for early-persist asserts.
	setSandboxIDs map[string]string
}

type leaseConfigCall struct {
	timeoutSeconds   int32
	thresholdSeconds int32
}

func newFakeBindings() *fakeBindings { return &fakeBindings{} }

func (f *fakeBindings) CreateSandboxBinding(_ context.Context, in store.CreateSandboxBindingInput) (store.SandboxBindingRead, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls++
	return store.SandboxBindingRead{ID: "binding-create-" + in.SandboxID, SandboxID: in.SandboxID}, nil
}

func (f *fakeBindings) ReserveSandboxBindingSlot(_ context.Context, in store.ReserveSandboxBindingSlotInput) (store.SandboxBindingRead, bool, error) {
	f.mu.Lock()
	f.reserveCalls++
	fn := f.reserveFn
	f.mu.Unlock()
	if fn != nil {
		return fn(in)
	}
	// Default: win, returning a fresh spawning row.
	return store.SandboxBindingRead{
		ID:          "binding-" + in.AgentID,
		WorkspaceID: in.WorkspaceID,
		Status:      store.SandboxBindingStatusSpawning,
		SandboxID:   "pending-" + in.AgentID,
	}, true, nil
}

func (f *fakeBindings) SetSpawningSandboxBindingSandboxID(_ context.Context, bindingID, sandboxID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.setSandboxIDs == nil {
		f.setSandboxIDs = map[string]string{}
	}
	f.setSandboxIDs[bindingID] = sandboxID
	return nil
}

func (f *fakeBindings) FinalizeSandboxBindingSpawning(_ context.Context, in store.FinalizeSandboxBindingSpawningInput) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finalizeCalls++
	f.lastFinalize = in
	return nil
}

func (f *fakeBindings) WaitForSandboxBindingActive(_ context.Context, workspaceID, agentID string, _ time.Duration) (store.SandboxBindingRead, error) {
	f.mu.Lock()
	f.waitCalls++
	fn := f.waitFn
	f.mu.Unlock()
	if fn != nil {
		return fn(workspaceID, agentID)
	}
	return store.SandboxBindingRead{}, store.ErrSandboxBindingFailed
}

func (f *fakeBindings) TouchSandboxBinding(_ context.Context, _ string) error { return nil }

func (f *fakeBindings) ConfigureSandboxBindingLease(_ context.Context, _ string, timeoutSeconds, thresholdSeconds int32, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.leaseConfigCalls = append(f.leaseConfigCalls, leaseConfigCall{timeoutSeconds: timeoutSeconds, thresholdSeconds: thresholdSeconds})
	return f.leaseConfigErr
}

func (f *fakeBindings) ClaimSandboxBindingsDueForAutoRenew(_ context.Context, _ time.Time, _ int32) ([]store.SandboxAutoRenewClaim, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]store.SandboxAutoRenewClaim(nil), f.claims...), nil
}

func (f *fakeBindings) CompleteSandboxBindingRenew(_ context.Context, bindingID string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.completedRenew = append(f.completedRenew, bindingID)
	return nil
}

func (f *fakeBindings) FailSandboxBindingRenew(_ context.Context, bindingID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failedRenew = append(f.failedRenew, bindingID)
	return nil
}

func (f *fakeBindings) GetActiveSandboxBindingByAgentID(_ context.Context, _ string) (store.SandboxBindingRead, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.activeBinding, f.activeFound, f.activeErr
}

func (f *fakeBindings) MarkSandboxBindingKilled(_ context.Context, bindingID, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.markKilled = append(f.markKilled, bindingID)
	if f.activeBinding.ID == bindingID {
		f.activeFound = false
	}
	return nil
}

func (f *fakeBindings) ReclaimAbandonedSandboxBinding(_ context.Context, _ string, _ time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reclaimCalls++
	return f.reclaimResult, f.reclaimErr
}

func TestE2BSandboxProvider_MaintainRenewsClaimedBindings(t *testing.T) {
	bindings := newFakeBindings()
	bindings.claims = []store.SandboxAutoRenewClaim{{
		BindingID: "binding-1", SandboxID: "sbx-1", AgentID: "pa-1", TimeoutSeconds: 3600,
	}}
	client := newFakeE2BClient()
	client.getInfoEndAt = time.Now().UTC().Add(time.Hour)
	p, err := NewE2BSandboxProvider(E2BProviderConfig{
		Client: client, Store: &fakeMinter{}, Registry: gateway.NewRegistry(),
		Binder: binding.NewInMemoryBinder(), Bindings: bindings,
		Template: "template", ServerURL: "https://parsar.example.com",
	})
	if err != nil {
		t.Fatalf("NewE2BSandboxProvider: %v", err)
	}
	renewed, err := p.Maintain(context.Background())
	if err != nil {
		t.Fatalf("Maintain: %v", err)
	}
	if renewed != 1 || client.renewCalls != 1 {
		t.Fatalf("Maintain renewed=%d provider calls=%d, want 1/1", renewed, client.renewCalls)
	}
	if len(bindings.completedRenew) != 1 || bindings.completedRenew[0] != "binding-1" {
		t.Fatalf("completed renewals = %v, want [binding-1]", bindings.completedRenew)
	}
}

// A transient renew failure (generic 5xx / network) must NOT permanently
// disable auto-renew: the claim stays retryable and the cached policy is
// left intact so the next maintenance scan tries again.
func TestE2BSandboxProvider_MaintainKeepsRetryingOnTransientRenewError(t *testing.T) {
	bindings := newFakeBindings()
	bindings.claims = []store.SandboxAutoRenewClaim{{
		BindingID: "binding-1", SandboxID: "sbx-1", AgentID: "pa-1", TimeoutSeconds: 3600,
	}}
	client := newFakeE2BClient()
	client.renewErr = errors.New("e2b 503 service unavailable")
	p, err := NewE2BSandboxProvider(E2BProviderConfig{
		Client: client, Store: &fakeMinter{}, Registry: gateway.NewRegistry(),
		Binder: binding.NewInMemoryBinder(), Bindings: bindings,
		Template: "template", ServerURL: "https://parsar.example.com",
	})
	if err != nil {
		t.Fatalf("NewE2BSandboxProvider: %v", err)
	}
	p.cache["pa-1"] = &sandboxEntry{autoRenew: true}
	renewed, err := p.Maintain(context.Background())
	if err != nil {
		t.Fatalf("Maintain: %v", err)
	}
	if renewed != 0 {
		t.Fatalf("renewed=%d, want 0", renewed)
	}
	if len(bindings.failedRenew) != 0 {
		t.Fatalf("transient error must not disable renewal; failed=%v", bindings.failedRenew)
	}
	if len(bindings.markKilled) != 0 {
		t.Fatalf("transient error must not free the slot; markKilled=%v", bindings.markKilled)
	}
	if !p.cache["pa-1"].autoRenew {
		t.Fatal("transient renew error must leave cached auto-renew enabled")
	}
}

// A provider that signals the renew operation is unsupported (405/501)
// disables auto-renew for that binding so the scan stops hammering it.
func TestE2BSandboxProvider_MaintainDisablesRenewWhenUnsupported(t *testing.T) {
	bindings := newFakeBindings()
	bindings.claims = []store.SandboxAutoRenewClaim{{
		BindingID: "binding-1", SandboxID: "sbx-1", AgentID: "pa-1", TimeoutSeconds: 3600,
	}}
	client := newFakeE2BClient()
	client.renewErr = &e2b.HTTPError{StatusCode: http.StatusNotImplemented, Message: "renew not implemented"}
	p, err := NewE2BSandboxProvider(E2BProviderConfig{
		Client: client, Store: &fakeMinter{}, Registry: gateway.NewRegistry(),
		Binder: binding.NewInMemoryBinder(), Bindings: bindings,
		Template: "template", ServerURL: "https://parsar.example.com",
	})
	if err != nil {
		t.Fatalf("NewE2BSandboxProvider: %v", err)
	}
	p.cache["pa-1"] = &sandboxEntry{autoRenew: true}
	renewed, err := p.Maintain(context.Background())
	if err != nil {
		t.Fatalf("Maintain: %v", err)
	}
	if renewed != 0 || len(bindings.failedRenew) != 1 || bindings.failedRenew[0] != "binding-1" {
		t.Fatalf("renewed=%d failed=%v, want 0/[binding-1]", renewed, bindings.failedRenew)
	}
	if p.cache["pa-1"].autoRenew {
		t.Fatal("unsupported renewal should disable cached auto-renew")
	}
}

// A renew against a sandbox e2b reports gone (404) frees the reservation
// slot so the next prompt cold-starts a fresh one instead of retrying a
// dead sandbox forever.
func TestE2BSandboxProvider_MaintainFreesSlotWhenSandboxGone(t *testing.T) {
	bindings := newFakeBindings()
	bindings.claims = []store.SandboxAutoRenewClaim{{
		BindingID: "binding-1", SandboxID: "sbx-1", AgentID: "pa-1", TimeoutSeconds: 3600,
	}}
	client := newFakeE2BClient()
	client.renewErr = &e2b.HTTPError{StatusCode: http.StatusNotFound, Message: "sandbox not found"}
	p, err := NewE2BSandboxProvider(E2BProviderConfig{
		Client: client, Store: &fakeMinter{}, Registry: gateway.NewRegistry(),
		Binder: binding.NewInMemoryBinder(), Bindings: bindings,
		Template: "template", ServerURL: "https://parsar.example.com",
	})
	if err != nil {
		t.Fatalf("NewE2BSandboxProvider: %v", err)
	}
	p.cache["pa-1"] = &sandboxEntry{autoRenew: true}
	renewed, err := p.Maintain(context.Background())
	if err != nil {
		t.Fatalf("Maintain: %v", err)
	}
	if renewed != 0 {
		t.Fatalf("renewed=%d, want 0", renewed)
	}
	if len(bindings.markKilled) != 1 || bindings.markKilled[0] != "binding-1" {
		t.Fatalf("gone sandbox should free the slot; markKilled=%v", bindings.markKilled)
	}
	if _, ok := p.cache["pa-1"]; ok {
		t.Fatal("gone sandbox should evict the cache entry")
	}
}

func TestE2BSandboxProvider_MaintainLeavesInterruptedClaimRetryable(t *testing.T) {
	bindings := newFakeBindings()
	bindings.claims = []store.SandboxAutoRenewClaim{{
		BindingID: "binding-1", SandboxID: "sbx-1", AgentID: "pa-1", TimeoutSeconds: 3600,
	}}
	client := newFakeE2BClient()
	client.renewErr = context.DeadlineExceeded
	p, err := NewE2BSandboxProvider(E2BProviderConfig{
		Client: client, Store: &fakeMinter{}, Registry: gateway.NewRegistry(),
		Binder: binding.NewInMemoryBinder(), Bindings: bindings,
		Template: "template", ServerURL: "https://parsar.example.com",
	})
	if err != nil {
		t.Fatalf("NewE2BSandboxProvider: %v", err)
	}
	if renewed, err := p.Maintain(context.Background()); err != nil || renewed != 0 {
		t.Fatalf("Maintain = %d, %v; want 0, nil", renewed, err)
	}
	if len(bindings.failedRenew) != 0 {
		t.Fatalf("interrupted claim must remain retryable; failed=%v", bindings.failedRenew)
	}
}

// TestE2BSandboxProvider_AcquireWinnerFinalizesReservation drives the
// happy winner path: Reserve wins, cold-start completes, Finalize is
// called with the real sandbox_id (not the placeholder), and
// MarkSandboxBindingKilled is never called.
func TestE2BSandboxProvider_AcquireWinnerFinalizesReservation(t *testing.T) {
	reg := gateway.NewRegistry()
	minter := &fakeMinter{}
	bindings := newFakeBindings()
	e2bClient := newFakeE2BClient()
	e2bClient.onConnect = func(_ e2b.Sandbox) {
		go func() {
			time.Sleep(5 * time.Millisecond)
			sess := gateway.NewSession(newFakeWSConn(), "dev-runtime-01", "wks-1", "0.0.0", reg, nil)
			reg.Register(sess)
		}()
	}

	p, err := NewE2BSandboxProvider(E2BProviderConfig{
		Client:    e2bClient,
		Store:     minter,
		Registry:  reg,
		Binder:    binding.NewInMemoryBinder(),
		Bindings:  bindings,
		Template:  "parsar-sandbox-e2b",
		ServerURL: "https://parsar.example.com",
	})
	if err != nil {
		t.Fatalf("provider construction: %v", err)
	}

	deviceID, err := p.Acquire(context.Background(), connector.PromptInput{
		AgentID:     "pa-1",
		WorkspaceID: "wks-1",
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if deviceID != "dev-runtime-01" {
		t.Fatalf("deviceID mismatch: got %q", deviceID)
	}
	if bindings.reserveCalls != 1 {
		t.Fatalf("expected 1 Reserve call, got %d", bindings.reserveCalls)
	}
	if bindings.finalizeCalls != 1 {
		t.Fatalf("expected 1 Finalize call, got %d", bindings.finalizeCalls)
	}
	if bindings.lastFinalize.SandboxID == "" ||
		strings.HasPrefix(bindings.lastFinalize.SandboxID, "pending-") {
		t.Fatalf("Finalize must carry real sandbox_id, not placeholder; got %q", bindings.lastFinalize.SandboxID)
	}
	if devIDInMeta, _ := bindings.lastFinalize.Metadata["device_id"].(string); devIDInMeta != "dev-runtime-01" {
		t.Fatalf("Finalize metadata.device_id mismatch: got %q", devIDInMeta)
	}
	if len(bindings.markKilled) != 0 {
		t.Fatalf("happy path must not call MarkSandboxBindingKilled; got %v", bindings.markKilled)
	}
	// Cross-pod path takes the place of the legacy CreateSandboxBinding.
	if bindings.createCalls != 0 {
		t.Fatalf("winner path should not use CreateSandboxBinding when reservation is held; got %d calls", bindings.createCalls)
	}
}

// TestE2BSandboxProvider_AcquireLoserReusesWinnerDevice covers the
// other half: Reserve loses because another pod already holds the
// row, Wait returns the winner's deviceID, and our pod returns it
// without minting, calling e2b Create, or running any commands.
func TestE2BSandboxProvider_AcquireLoserReusesWinnerDevice(t *testing.T) {
	reg, _ := newTestRegistryWithDevice(t, "dev-runtime-winner")
	minter := &fakeMinter{}
	e2bClient := newFakeE2BClient()
	bindings := newFakeBindings()
	expiresAt := time.Now().UTC().Add(40 * time.Minute)
	bindings.activeFound = true
	bindings.activeBinding = store.SandboxBindingRead{
		ID:                        "binding-winner",
		SandboxID:                 "sb-winner",
		TimeoutSeconds:            3600,
		AutoRenewThresholdSeconds: 0,
		ExpiresAt:                 &expiresAt,
	}
	bindings.reserveFn = func(in store.ReserveSandboxBindingSlotInput) (store.SandboxBindingRead, bool, error) {
		// Another pod won the slot first.
		return store.SandboxBindingRead{
			ID:          "binding-winner",
			WorkspaceID: in.WorkspaceID,
			Status:      store.SandboxBindingStatusSpawning,
			SandboxID:   "pending-x",
		}, false, nil
	}
	bindings.waitFn = func(_, _ string) (store.SandboxBindingRead, error) {
		return store.SandboxBindingRead{
			ID:        "binding-winner",
			Status:    store.SandboxBindingStatusActive,
			SandboxID: "sb-winner",
			Metadata: map[string]any{
				"device_id": "dev-runtime-winner",
			},
		}, nil
	}

	p, err := NewE2BSandboxProvider(E2BProviderConfig{
		Client:    e2bClient,
		Store:     minter,
		Registry:  reg,
		Binder:    binding.NewInMemoryBinder(),
		Bindings:  bindings,
		Template:  "parsar-sandbox-e2b",
		ServerURL: "https://parsar.example.com",
	})
	if err != nil {
		t.Fatalf("provider construction: %v", err)
	}

	deviceID, err := p.Acquire(context.Background(), connector.PromptInput{
		AgentID:     "pa-1",
		WorkspaceID: "wks-1",
		AgentConfig: map[string]any{"sandbox_ttl": "30m", "sandbox_auto_renew": true},
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if deviceID != "dev-runtime-winner" {
		t.Fatalf("loser should reuse winner deviceID, got %q", deviceID)
	}
	if minter.calls != 0 {
		t.Fatalf("loser must NOT mint a new runtime; got %d calls", minter.calls)
	}
	if e2bClient.createCalls != 0 {
		t.Fatalf("loser must NOT create an E2B sandbox; got %d calls", e2bClient.createCalls)
	}
	if len(e2bClient.runCommands) != 0 {
		t.Fatalf("loser must NOT run any commands inside a sandbox; got %v", e2bClient.runCommands)
	}
	if bindings.reserveCalls != 1 {
		t.Fatalf("expected 1 Reserve call, got %d", bindings.reserveCalls)
	}
	if bindings.waitCalls != 1 {
		t.Fatalf("expected 1 Wait call, got %d", bindings.waitCalls)
	}
	if bindings.finalizeCalls != 0 {
		t.Fatalf("loser must not Finalize; got %d", bindings.finalizeCalls)
	}
	if len(bindings.leaseConfigCalls) != 1 || bindings.leaseConfigCalls[0].timeoutSeconds != 1800 || bindings.leaseConfigCalls[0].thresholdSeconds != 450 {
		t.Fatalf("loser lease sync calls=%+v, want one 1800s/450s call", bindings.leaseConfigCalls)
	}
}

func TestE2BSandboxProvider_ReclaimAbandonedSpawnCASLostDoesNotKill(t *testing.T) {
	bindings := newFakeBindings()
	bindings.reclaimResult = false
	client := newFakeE2BClient()
	p := &E2BSandboxProvider{cfg: E2BProviderConfig{
		Client: client, Bindings: bindings, Log: discardLogger(),
	}}

	reclaimed, _, _, err := p.reclaimAbandonedSpawn(
		context.Background(),
		connector.PromptInput{AgentID: "pa-1", WorkspaceID: "wks-1"},
		"agent_daemon:pa-1", "template",
		store.SandboxBindingRead{
			ID: "binding-old", Status: store.SandboxBindingStatusSpawning,
			SandboxID: "sbx-old", CreatedAt: time.Now().UTC().Add(-10 * time.Minute),
		},
	)
	if err != nil || reclaimed {
		t.Fatalf("CAS-lost reclaim = %v, err=%v; want false, nil", reclaimed, err)
	}
	if client.killCalls != 0 || bindings.reserveCalls != 0 {
		t.Fatalf("CAS loser must not kill or reserve: kill=%d reserve=%d", client.killCalls, bindings.reserveCalls)
	}
}

func TestE2BSandboxProvider_ReclaimAbandonedSpawnCASWinnerKillsThenReserves(t *testing.T) {
	bindings := newFakeBindings()
	bindings.reclaimResult = true
	client := newFakeE2BClient()
	p := &E2BSandboxProvider{cfg: E2BProviderConfig{
		Client: client, Bindings: bindings, Log: discardLogger(),
	}}

	reclaimed, row, won, err := p.reclaimAbandonedSpawn(
		context.Background(),
		connector.PromptInput{AgentID: "pa-1", WorkspaceID: "wks-1"},
		"agent_daemon:pa-1", "template",
		store.SandboxBindingRead{
			ID: "binding-old", Status: store.SandboxBindingStatusSpawning,
			SandboxID: "sbx-old", CreatedAt: time.Now().UTC().Add(-10 * time.Minute),
		},
	)
	if err != nil || !reclaimed || !won || row.ID == "" {
		t.Fatalf("CAS-winner reclaim=%v won=%v row=%+v err=%v", reclaimed, won, row, err)
	}
	if client.killCalls != 1 || bindings.reserveCalls != 1 {
		t.Fatalf("CAS winner should kill then reserve once: kill=%d reserve=%d", client.killCalls, bindings.reserveCalls)
	}
}

// TestE2BSandboxProvider_AcquireWinnerFailureReleasesReservation
// covers the cold-start failure path on the winner side: when
// daemon dial-in times out (or any cold-start step fails), the
// reservation row must be marked killed_error so loser pods are
// released instead of hanging until SandboxAcquireTimeout.
func TestE2BSandboxProvider_AcquireWinnerFailureReleasesReservation(t *testing.T) {
	reg := gateway.NewRegistry()
	minter := &fakeMinter{}
	bindings := newFakeBindings()
	e2bClient := newFakeE2BClient()
	// onConnect is intentionally NOT set — the daemon never dials
	// back, so WaitForDevice times out and we hit the failure path.

	p, err := NewE2BSandboxProvider(E2BProviderConfig{
		Client:    e2bClient,
		Store:     minter,
		Registry:  reg,
		Binder:    binding.NewInMemoryBinder(),
		Bindings:  bindings,
		Template:  "parsar-sandbox-e2b",
		ServerURL: "https://parsar.example.com",
	})
	if err != nil {
		t.Fatalf("provider construction: %v", err)
	}

	// Tighten the wait timeout so the test runs in <2s.
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	_, err = p.Acquire(ctx, connector.PromptInput{
		AgentID:     "pa-1",
		WorkspaceID: "wks-1",
	})
	if err == nil {
		t.Fatalf("expected Acquire failure when daemon never dials in")
	}
	if !errors.Is(err, ErrSandboxAcquireFailed) {
		t.Fatalf("expected ErrSandboxAcquireFailed wrap, got: %v", err)
	}
	if bindings.reserveCalls != 1 {
		t.Fatalf("expected 1 Reserve call, got %d", bindings.reserveCalls)
	}
	if bindings.finalizeCalls != 0 {
		t.Fatalf("failure path must not Finalize; got %d", bindings.finalizeCalls)
	}
	if len(bindings.markKilled) != 1 || bindings.markKilled[0] != "binding-pa-1" {
		t.Fatalf("failure path must MarkSandboxBindingKilled once for our reservation; got %v", bindings.markKilled)
	}
}

// fakeOwnerChecker is a scriptable DeviceOwnerChecker used by the
// checkDeviceAlive tests. It records the deviceID it was asked about so
// the tests can assert whether the fast path consulted it.
type fakeOwnerChecker struct {
	mu       sync.Mutex
	calls    int
	lastID   string
	response struct {
		owner store.AgentDaemonDeviceOwnerRead
		found bool
		err   error
	}
}

func (f *fakeOwnerChecker) GetAgentDaemonDeviceOwner(_ context.Context, deviceID string) (store.AgentDaemonDeviceOwnerRead, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastID = deviceID
	return f.response.owner, f.response.found, f.response.err
}

// TestCheckDeviceAlive_LocalHit: cached entry says the daemon landed on
// this pod and Registry confirms it. The OwnerChecker must not be touched
// — the fast path's local case has to stay a pure in-memory check.
func TestCheckDeviceAlive_LocalHit(t *testing.T) {
	reg := gateway.NewRegistry()
	conn := newFakeWSConn()
	sess := gateway.NewSession(conn, "dev-1", "wks-1", "0.0.0", reg, nil)
	reg.Register(sess)

	checker := &fakeOwnerChecker{}
	p := &E2BSandboxProvider{cfg: E2BProviderConfig{
		Registry:     reg,
		SelfPodID:    "pod-A",
		OwnerChecker: checker,
	}}

	if alive := p.checkDeviceAlive(context.Background(), "dev-1", "pod-A"); !alive {
		t.Fatalf("local Registry hit must be alive")
	}
	if checker.calls != 0 {
		t.Fatalf("OwnerChecker must not be consulted on local hit; calls=%d", checker.calls)
	}
}

// TestCheckDeviceAlive_RemoteHit: cached entry says the daemon landed on
// a sibling pod. The fast path must skip the local Registry (which will
// always miss) and consult the OwnerChecker. This is the core bug-fix
// case — before the fix, the local lookup miss caused evict + cold-start
// and tore down the remote session.
func TestCheckDeviceAlive_RemoteHit(t *testing.T) {
	reg := gateway.NewRegistry() // empty on this pod
	checker := &fakeOwnerChecker{}
	checker.response.found = true
	checker.response.owner = store.AgentDaemonDeviceOwnerRead{
		OwnerPodID:     "pod-B",
		Status:         store.AgentDaemonOwnerStatusConnected,
		LeaseExpiresAt: time.Now().UTC().Add(time.Minute),
	}
	p := &E2BSandboxProvider{cfg: E2BProviderConfig{
		Registry:     reg,
		SelfPodID:    "pod-A",
		OwnerChecker: checker,
		Log:          discardLogger(),
	}}

	if alive := p.checkDeviceAlive(context.Background(), "dev-2", "pod-B"); !alive {
		t.Fatalf("remote connected device must be alive")
	}
	if checker.calls != 1 || checker.lastID != "dev-2" {
		t.Fatalf("OwnerChecker must be consulted exactly once for dev-2; calls=%d lastID=%q", checker.calls, checker.lastID)
	}
}

// TestCheckDeviceAlive_LocalMissFallback: cached entry claims local but
// the Registry has lost the session (e.g. brief reconnect). If the
// OwnerStore disagrees and shows the device is still owned somewhere with
// a valid lease, treat as alive. This absorbs flaky local state without
// triggering an evict.
func TestCheckDeviceAlive_LocalMissFallback(t *testing.T) {
	reg := gateway.NewRegistry() // empty
	checker := &fakeOwnerChecker{}
	checker.response.found = true
	checker.response.owner = store.AgentDaemonDeviceOwnerRead{
		OwnerPodID:     "pod-A",
		Status:         store.AgentDaemonOwnerStatusConnected,
		LeaseExpiresAt: time.Now().UTC().Add(time.Minute),
	}
	p := &E2BSandboxProvider{cfg: E2BProviderConfig{
		Registry:     reg,
		SelfPodID:    "pod-A",
		OwnerChecker: checker,
		Log:          discardLogger(),
	}}

	if alive := p.checkDeviceAlive(context.Background(), "dev-3", "pod-A"); !alive {
		t.Fatalf("local miss + remote-store-confirms-connected must be alive")
	}
	if checker.calls != 1 {
		t.Fatalf("OwnerChecker must be consulted once; calls=%d", checker.calls)
	}
}

// TestCheckDeviceAlive_NotFound: cached entry is stale and the
// OwnerStore agrees the device is gone. Must report dead so the caller
// proceeds to evict + cold-start.
func TestCheckDeviceAlive_NotFound(t *testing.T) {
	reg := gateway.NewRegistry()
	checker := &fakeOwnerChecker{} // response.found = false by default
	p := &E2BSandboxProvider{cfg: E2BProviderConfig{
		Registry:     reg,
		SelfPodID:    "pod-A",
		OwnerChecker: checker,
		Log:          discardLogger(),
	}}

	if alive := p.checkDeviceAlive(context.Background(), "dev-4", "pod-B"); alive {
		t.Fatalf("not-found in OwnerStore must report dead")
	}
}

// TestCheckDeviceAlive_LeaseExpired: an expired lease is not a valid
// liveness signal. The session may have died and just not been cleaned
// up; conservatively treat as dead.
func TestCheckDeviceAlive_LeaseExpired(t *testing.T) {
	reg := gateway.NewRegistry()
	checker := &fakeOwnerChecker{}
	checker.response.found = true
	checker.response.owner = store.AgentDaemonDeviceOwnerRead{
		OwnerPodID:     "pod-B",
		Status:         store.AgentDaemonOwnerStatusConnected,
		LeaseExpiresAt: time.Now().UTC().Add(-time.Second), // expired
	}
	p := &E2BSandboxProvider{cfg: E2BProviderConfig{
		Registry:     reg,
		SelfPodID:    "pod-A",
		OwnerChecker: checker,
		Log:          discardLogger(),
	}}

	if alive := p.checkDeviceAlive(context.Background(), "dev-5", "pod-B"); alive {
		t.Fatalf("expired lease must report dead")
	}
}

// TestCheckDeviceAlive_StatusNotConnected: draining / expired statuses
// are explicit non-connected states. Must not survive the check even if
// the lease is still in the future.
func TestCheckDeviceAlive_StatusNotConnected(t *testing.T) {
	reg := gateway.NewRegistry()
	checker := &fakeOwnerChecker{}
	checker.response.found = true
	checker.response.owner = store.AgentDaemonDeviceOwnerRead{
		OwnerPodID:     "pod-B",
		Status:         store.AgentDaemonOwnerStatusDraining,
		LeaseExpiresAt: time.Now().UTC().Add(time.Minute),
	}
	p := &E2BSandboxProvider{cfg: E2BProviderConfig{
		Registry:     reg,
		SelfPodID:    "pod-A",
		OwnerChecker: checker,
		Log:          discardLogger(),
	}}

	if alive := p.checkDeviceAlive(context.Background(), "dev-6", "pod-B"); alive {
		t.Fatalf("non-connected status must report dead")
	}
}

// TestCheckDeviceAlive_SinglePodMode: OwnerChecker is nil (no DB-backed
// store wired). Behaviour must collapse to the original Registry-only
// check — no DB round-trip, no nil-deref, local-miss is authoritative.
func TestCheckDeviceAlive_SinglePodMode(t *testing.T) {
	reg := gateway.NewRegistry()
	p := &E2BSandboxProvider{cfg: E2BProviderConfig{
		Registry:  reg,
		SelfPodID: "", // single-pod
		// OwnerChecker intentionally nil
		Log: discardLogger(),
	}}

	// Empty cachedOwnerPodID + empty SelfPodID → treated as local.
	if alive := p.checkDeviceAlive(context.Background(), "dev-7", ""); alive {
		t.Fatalf("single-pod miss must report dead")
	}

	// Add the session to the Registry and retry — must now be alive.
	conn := newFakeWSConn()
	sess := gateway.NewSession(conn, "dev-7", "wks-1", "0.0.0", reg, nil)
	reg.Register(sess)
	if alive := p.checkDeviceAlive(context.Background(), "dev-7", ""); !alive {
		t.Fatalf("single-pod hit must report alive")
	}
}

// TestCheckDeviceAlive_OwnerCheckerError: a transient DB error must fail
// closed (treat as dead). This is the same conservative posture the
// pre-fix code had — we never want to keep returning a deviceID we
// can't confirm.
func TestCheckDeviceAlive_OwnerCheckerError(t *testing.T) {
	reg := gateway.NewRegistry()
	checker := &fakeOwnerChecker{}
	checker.response.err = errors.New("db down")
	p := &E2BSandboxProvider{cfg: E2BProviderConfig{
		Registry:     reg,
		SelfPodID:    "pod-A",
		OwnerChecker: checker,
		Log:          discardLogger(),
	}}

	if alive := p.checkDeviceAlive(context.Background(), "dev-8", "pod-B"); alive {
		t.Fatalf("OwnerChecker error must fail closed (report dead)")
	}
}
