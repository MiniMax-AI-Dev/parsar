package e2b

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/google/uuid"
)

// This suite uses actual E2B VMs. Public real-model acceptance is separate; a
// completed bootstrap intentionally does not assert that its daemon authenticated.
func TestRealE2BLifecycle(t *testing.T) {
	keyFile, template := os.Getenv("PARSAR_E2B_TEST_KEY_FILE"), os.Getenv("PARSAR_E2B_TEST_TEMPLATE")
	if keyFile == "" || template == "" {
		t.Skip("explicit real E2B account and qualified pinned template required")
	}
	key, e := os.ReadFile(keyFile)
	if e != nil {
		t.Fatal("cannot read private E2B key")
	}
	config := Config{InstallationID: uuid.NewString(), APIKey: strings.TrimSpace(string(key)), Template: template, LeaseSeconds: 7200}
	p, e := New(config)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	b := sandbox.Bootstrap{Reference: sandbox.Reference{TenantID: uuid.NewString(), EnvironmentID: uuid.NewString(), AllocationID: uuid.NewString()}, SessionID: uuid.NewString(), DeviceID: uuid.NewString(), CoreURL: "https://example.com/api/v1", Credential: uuid.NewString(), NetworkAccess: "enabled"}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if e := p.Kill(cleanup, b.Reference); e != nil {
			t.Error("real E2B cleanup", e)
		}
	})
	info, e := p.Create(ctx, b)
	if e != nil {
		t.Fatal("real create", e)
	}
	if info.ProviderID == "" || info.State != "running" || !info.BootstrapComplete {
		t.Fatal("incomplete real bootstrap")
	}
	t.Log("real Create and completed bootstrap")
	protection, e := p.RunCommand(ctx, b.Reference, sandbox.Command{Args: []string{"/usr/bin/python3", "-c", `import os, subprocess
for path in ['/usr/local/bin/parsar-daemon', '/opt/parsar-e2b/init.py', '/usr/bin/envd']:
    assert os.stat(path).st_uid == 0 and os.stat(path).st_mode & 0o022 == 0
    try:
        fd = os.open(path, os.O_WRONLY | os.O_APPEND)
    except PermissionError:
        pass
    else:
        os.close(fd)
        raise AssertionError('runtime can modify trusted code')
for path in ['/usr/local', '/usr/local/bin', '/opt/parsar-e2b']:
    try:
        fd = os.open(path + '/e2b-unsafe-write', os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    except PermissionError:
        pass
    else:
        os.close(fd)
        raise AssertionError('runtime can replace trusted code')
r = subprocess.run(['su', 'user', '-c', 'id -u'], input='', text=True, capture_output=True, timeout=5)
assert r.returncode != 0, 'runtime can assume the passwordless privileged account'
print('protected')`}})
	if e != nil || protection.ExitCode != 0 || protection.Stdout != "protected\n" {
		t.Fatal("real Runtime executable/account protection failed", e)
	}
	t.Log("real unprivileged writes and privileged account transition denied")
	fresh, e := New(config)
	if e != nil {
		t.Fatal(e)
	}
	observed, e := fresh.GetInfo(ctx, b.Reference)
	if e != nil || observed != info {
		t.Fatal("fresh provider lost allocation", e)
	}
	if _, e = p.Create(ctx, b); !errors.Is(e, sandbox.ErrExists) {
		t.Fatal("duplicate allocation admitted", e)
	}
	foreign := b.Reference
	foreign.TenantID = uuid.NewString()
	if _, e = p.GetInfo(ctx, foreign); !errors.Is(e, sandbox.ErrNotFound) {
		t.Fatal("foreign inspection", e)
	}
	if _, e = p.RunCommand(ctx, foreign, sandbox.Command{Args: []string{"/usr/bin/id"}}); !errors.Is(e, sandbox.ErrNotFound) {
		t.Fatal("foreign initialization", e)
	}
	if e = p.Kill(ctx, foreign); e != nil {
		t.Fatal(e)
	}
	if _, e = p.GetInfo(ctx, b.Reference); e != nil {
		t.Fatal("foreign cleanup changed owner", e)
	}
	t.Log("restart lookup, duplicate prevention and foreign ownership")
	var before, after struct {
		End time.Time `json:"endAt"`
	}
	_, e = p.request(ctx, http.MethodGet, "/sandboxes/"+info.ProviderID, nil, &before)
	if e != nil {
		t.Fatal(e)
	}
	renewed, e := p.Renew(ctx, b.Reference)
	if e != nil || renewed.ProviderID != info.ProviderID {
		t.Fatal("renew replaced allocation", e)
	}
	_, e = p.request(ctx, http.MethodGet, "/sandboxes/"+info.ProviderID, nil, &after)
	if e != nil || !after.End.After(before.End) {
		t.Fatal("lease did not advance", e)
	}
	result, e := p.RunCommand(ctx, b.Reference, sandbox.Command{Args: []string{"/usr/bin/python3", "-c", "import os,sys; print(os.getuid()); print(sys.argv[1]); print('stderr-proof',file=sys.stderr); sys.exit(7)", "literal;$(not-a-shell)"}, Directory: "/workspace"})
	if e != nil || result.ExitCode != 7 || result.Stdout != "1000\nliteral;$(not-a-shell)\n" || result.Stderr != "stderr-proof\n" {
		t.Fatal("real argv/user/exit/output differ", e)
	}
	t.Log("real Renew and unprivileged argv-preserving RunCommand")
	a, e := p.inspect(ctx, b.Reference)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.file(ctx, a, bootstrapReceipt, []byte(`{"TenantID":"foreign"}`)); e != nil {
		t.Fatal(e)
	}
	if _, e = p.GetInfo(ctx, b.Reference); !errors.Is(e, sandbox.ErrOwnership) {
		t.Fatal("bad bootstrap receipt accepted", e)
	}
	uncertain, stop := context.WithTimeout(ctx, 5*time.Second)
	_, e = p.RunCommand(uncertain, b.Reference, sandbox.Command{Args: []string{"/usr/bin/python3", "-c", "import time; open('/workspace/command-started','w').write('started'); time.sleep(30)"}})
	stop()
	if !errors.Is(e, sandbox.ErrCommandUnconfirmed) && !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal("uncertain command reported success", e)
	}
	started, readErr := p.RunCommand(ctx, b.Reference, sandbox.Command{Args: []string{"/bin/cat", "/workspace/command-started"}})
	if readErr != nil || started.Stdout != "started" {
		t.Fatal("timeout fixture never started its actual process", readErr)
	}
	if e = p.Kill(ctx, b.Reference); e != nil {
		t.Fatal("real Kill", e)
	}
	if _, e = p.GetInfo(ctx, b.Reference); !errors.Is(e, sandbox.ErrNotFound) {
		t.Fatal("removal not confirmed", e)
	}
	if e = p.Kill(ctx, b.Reference); e != nil {
		t.Fatal("repeated Kill", e)
	}
	t.Log("invalid receipt, uncertain command and confirmed idempotent cleanup")

	// Model a lost Create acknowledgement with a real cloud allocation that has
	// never received bootstrap. Observation must not launch a daemon or recreate it.
	b.AllocationID = uuid.NewString()
	metadata := p.metadata(b.Reference)
	var partial allocation
	_, e = p.request(ctx, http.MethodPost, "/sandboxes", map[string]any{"templateID": config.Template, "timeout": 120, "secure": true, "metadata": metadata}, &partial)
	if e != nil {
		t.Fatal(e)
	}
	pending, e := fresh.GetInfo(ctx, b.Reference)
	if e != nil || pending.ProviderID != partial.ID || pending.BootstrapComplete {
		t.Fatal("running VM mistaken for initialized Runtime", e)
	}
	if _, e = p.Create(ctx, b); !errors.Is(e, sandbox.ErrExists) {
		t.Fatal("unconfirmed Create replayed", e)
	}
	if e = p.Kill(ctx, b.Reference); e != nil {
		t.Fatal(e)
	}
	t.Log("lost response recovery observes incomplete bootstrap without replay")
}
