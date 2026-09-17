package codex

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
	"testing"
)

func TestFailedReadPreparationRetainsUnreapedOwner(t *testing.T) {
	state := t.TempDir()
	cmd := exec.Command("sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	rpc := NewJSONRPCClient(JSONRPCConfig{})
	rpc.cmd, rpc.alive = cmd, true
	var reap sync.Once
	t.Cleanup(func() { _ = cmd.Process.Kill(); reap.Do(rpc.waitChild) })
	_, cancel := context.WithCancel(t.Context())
	cleanup := readPreparationCleanup(rpc, sync.OnceFunc(func() { _ = os.RemoveAll(state) }))
	p := &Prepared{workspaceReadOnly: true, session: &Session{rpc: rpc, cancelFn: cancel}, plan: SessionPlan{Cwd: state, Cleanup: cleanup}}
	cause := errors.New("controlled initialization failure")
	owner, err := p.preparationFailed(cause)
	if owner != p || !errors.Is(err, cause) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("construction failure discarded an unconfirmed resource", err)
	}
	if _, err := os.Stat(state); err != nil {
		t.Fatal("unreaped owner lost temporary state", err)
	}
	reap.Do(rpc.waitChild)
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatal("confirmed cleanup retained temporary state", err)
	}
}
