package codex

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestJSONRPCClientCloseCanRetryUnreapedChild(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestJSONRPCClientFakeCodexProcess$", "--")
	cmd.Env = append(os.Environ(), "CODEX_RPC_FAKE_PROCESS=1", "GORACE=atexit_sleep_ms=0")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	client := NewJSONRPCClient(JSONRPCConfig{})
	input := &countedCloseWriter{WriteCloser: stdin}
	client.cmd, client.stdin, client.stdout, client.alive = cmd, input, stdout, true
	var reap sync.Once
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		reap.Do(client.waitChild)
	})
	if _, err := io.WriteString(stdin, "{\"id\":\"close-test\"}\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := bufio.NewReader(stdout).ReadBytes('\n'); err != nil {
		t.Fatal(err)
	}
	pending := &pendingRequest{resp: make(chan rpcResponse, 1)}
	client.pending["pending"] = pending

	// Keep Wait under test control so a killed child remains unacknowledged.
	if err := client.Close(); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("close before reaping: got %v, want deadline exceeded", err)
	}
	if client.Alive() {
		t.Fatal("closed client still admits requests")
	}
	select {
	case response := <-pending.resp:
		if response.err == nil {
			t.Fatal("pending request succeeded after close")
		}
	default:
		t.Fatal("close left a pending request")
	}
	select {
	case <-client.Done():
		t.Fatal("Done closed before reaping")
	default:
	}

	closeConcurrently := func(wantTimeout bool) {
		t.Helper()
		results := make(chan error, 4)
		for range cap(results) {
			go func() { results <- client.Close() }()
		}
		deadline := time.NewTimer(8 * time.Second)
		defer deadline.Stop()
		for range cap(results) {
			select {
			case err := <-results:
				if wantTimeout && !errors.Is(err, context.DeadlineExceeded) || !wantTimeout && err != nil {
					t.Fatalf("concurrent Close: timeout=%t, error=%v", wantTimeout, err)
				}
			case <-deadline.C:
				t.Fatal("concurrent Close did not finish")
			}
		}
	}
	closeConcurrently(true)
	reap.Do(client.waitChild)
	closeConcurrently(false)
	if client.cmd != cmd || cmd.ProcessState == nil {
		t.Fatal("Close did not retain and reap its original child")
	}
	if got := input.closes.Load(); got != 1 {
		t.Fatalf("stdin closed %d times, want one shutdown initiation", got)
	}
}

type countedCloseWriter struct {
	io.WriteCloser
	closes atomic.Int32
}

func (w *countedCloseWriter) Close() error {
	w.closes.Add(1)
	return w.WriteCloser.Close()
}
