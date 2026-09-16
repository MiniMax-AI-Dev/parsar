package codex

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestCancelReleasesBlockedControlWrite(t *testing.T) {
	for _, concurrent := range []bool{false, true} {
		name := "interrupt"
		if concurrent {
			name = "another write"
		}
		t.Run(name, func(t *testing.T) {
			s, client, server := cancellationTestSession(t)
			s.setThreadID("native-thread")
			s.onTurnStarted(json.RawMessage(`{"threadId":"native-thread","turn":{"id":"native-turn"}}`))
			_, peer, peerServer := cancellationTestSession(t)
			cancelDone := make(chan struct{})
			writeDone := make(chan struct{})
			var writeErr error
			t.Cleanup(func() {
				_ = client.Close()
				select {
				case <-cancelDone:
				case <-time.After(4 * time.Second):
					t.Error("cancellation did not stop after test cleanup")
				}
				if concurrent {
					select {
					case <-writeDone:
					case <-time.After(4 * time.Second):
						t.Error("concurrent writer did not stop after test cleanup")
					}
				}
			})
			if concurrent {
				go func() {
					writeErr = client.Notify("blocked", nil)
					close(writeDone)
				}()
				readControlPrefix(t, server.FromClient)
			}
			go func() {
				defer close(cancelDone)
				if err := s.Cancel(context.Background()); err != nil {
					t.Errorf("best-effort cancellation: %v", err)
				}
			}()
			if !concurrent {
				readControlPrefix(t, server.FromClient)
			}
			// The pipe remains undrained after one byte, so the write cannot complete.
			select {
			case <-cancelDone:
			case <-time.After(4 * time.Second):
				t.Fatal("blocked control write exceeded the interrupt budget without cleanup")
			}
			if client.Alive() || s.cancelCtx.Err() == nil {
				t.Fatal("cancellation did not close its client and context")
			}
			client.pendingMu.Lock()
			pending := len(client.pending)
			client.pendingMu.Unlock()
			if pending != 0 {
				t.Fatal("blocked cancellation left pending requests")
			}
			if concurrent {
				select {
				case <-writeDone:
					if writeErr == nil {
						t.Fatal("partial concurrent write reported success")
					}
				case <-time.After(time.Second):
					t.Fatal("blocked concurrent writer was not released")
				}
			}
			replied := make(chan error, 1)
			go func() {
				_, err := SendCannedResponse(t.Context(), peerServer, "alive")
				replied <- err
			}()
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			result, err := peer.Request(ctx, "echo", nil)
			if err != nil || string(result) != `"alive"` || !peer.Alive() {
				t.Fatal("cancellation affected an independent client", err)
			}
			if err := <-replied; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func readControlPrefix(t *testing.T, reader io.Reader) {
	t.Helper()
	read := make(chan error, 1)
	go func() {
		var prefix [1]byte
		_, err := io.ReadFull(reader, prefix[:])
		read <- err
	}()
	select {
	case err := <-read:
		if err != nil {
			t.Fatal("control write did not start", err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("control write did not enter the pipe")
	}
}

func TestCancelReleasesBlockedNativeProcess(t *testing.T) {
	client := NewJSONRPCClient(JSONRPCConfig{
		Binary: os.Args[0], ExtraArgs: []string{"-test.run=TestJSONRPCClientFakeCodexProcess", "--"},
		Env:    append(os.Environ(), "CODEX_RPC_FAKE_PROCESS=1", "CODEX_RPC_FAKE_BLOCK_WRITE=1", "GORACE=atexit_sleep_ms=0"),
		LogTag: "cancel-blocked-process", RequestTimeout: 2 * time.Second,
	})
	t.Cleanup(func() { _ = client.Close() })
	blocked := make(chan struct{})
	client.OnNotification("test/write_blocked", func(json.RawMessage) { close(blocked) })
	if _, err := client.Start(t.Context(), InitializeParams{ClientInfo: InitializeClientInfo{Name: "test", Version: "0"}}); err != nil {
		t.Fatal(err)
	}
	written := make(chan error, 1)
	go func() { written <- client.Notify("blocked", strings.Repeat("x", 1<<20)) }()
	t.Cleanup(func() {
		_ = client.Close()
		select {
		case <-written:
		case <-time.After(4 * time.Second):
			t.Error("native pipe writer did not stop during cleanup")
		}
	})
	select {
	case <-blocked:
	case <-time.After(4 * time.Second):
		t.Fatal("native child did not stop reading the oversized frame")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &Session{rpc: client, cancelCtx: ctx, cancelFn: cancel,
		cfg: defaultSessionConfig(), interactions: newPendingCodexInteractions(), bufs: NewItemBuffers()}
	s.setThreadID("native-thread")
	s.onTurnStarted(json.RawMessage(`{"threadId":"native-thread","turn":{"id":"native-turn"}}`))
	cancelDone := make(chan struct{})
	go func() { _ = s.Cancel(context.Background()); close(cancelDone) }()
	t.Cleanup(func() {
		_ = client.Close()
		select {
		case <-cancelDone:
		case <-time.After(4 * time.Second):
			t.Error("native cancellation did not stop during cleanup")
		}
	})
	select {
	case <-cancelDone:
	case <-time.After(4 * time.Second):
		t.Fatal("blocked native stdin prevented cancellation cleanup")
	}
	select {
	case <-client.Done():
	case <-time.After(time.Second):
		t.Fatal("cancelled native child was not reaped")
	}
	if client.Alive() || client.cmd.ProcessState == nil || ctx.Err() == nil {
		t.Fatal("native process or cancellation context remained active")
	}
	// The cleanup consumes the writer's result after proving it was released.
	select {
	case err := <-written:
		written <- err
		if err == nil {
			t.Fatal("undrained native frame reported success")
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled native writer remained blocked")
	}
}
