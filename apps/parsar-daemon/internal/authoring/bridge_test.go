package authoring

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type testSender struct{ frames chan proto.Envelope }

func (s testSender) Send(ctx context.Context, env proto.Envelope) error {
	select {
	case s.frames <- env:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestBridgeUsesRunAttributionAndClosesAccess(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "pa-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(home)
	t.Setenv("HOME", home)
	sender := testSender{frames: make(chan proto.Envelope, 1)}
	b := New(sender)
	path, release, err := b.Listen(t.Context(), "run-a")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if filepath.Dir(path) != filepath.Join(home, ".parsar", "authoring") {
		t.Fatal("socket outside Parsar state")
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("socket permissions: %v %v", info, err)
	}
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if err := json.NewEncoder(conn).Encode(proto.AuthoringRequestPayload{RequestID: "client-supplied", Operation: proto.AuthoringContext}); err != nil {
		t.Fatal(err)
	}
	var frame proto.Envelope
	select {
	case frame = <-sender.frames:
	case <-time.After(time.Second):
		t.Fatal("request not forwarded")
	}
	var request proto.AuthoringRequestPayload
	if frame.DecodePayload(&request) != nil || frame.ID != "run-a" || request.RequestID == "client-supplied" || frame.Type != proto.TypeAuthoringRequest {
		t.Fatalf("wrong attribution: %+v", frame)
	}
	wrong, _ := proto.NewEnvelope(proto.TypeAuthoringResponse, "run-b", proto.AuthoringResponsePayload{RequestID: request.RequestID, Data: json.RawMessage(`"wrong"`)})
	b.Deliver(wrong)
	good, _ := proto.NewEnvelope(proto.TypeAuthoringResponse, "run-a", proto.AuthoringResponsePayload{RequestID: request.RequestID, Data: json.RawMessage(`"right"`)})
	b.Deliver(good)
	var response proto.AuthoringResponsePayload
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if string(response.Data) != `"right"` {
		t.Fatalf("wrong response: %+v", response)
	}
	release()
	if _, err := net.DialTimeout("unix", path, time.Second); err == nil {
		t.Fatal("completed run still accepts commands")
	}
}

func TestBridgeCancellationClearsWaiters(t *testing.T) {
	sender := testSender{frames: make(chan proto.Envelope, 1)}
	b := New(sender)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := b.request(ctx, "run", proto.AuthoringRequestPayload{Operation: proto.AuthoringSkillList})
		done <- err
	}()
	<-sender.frames
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled request succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled request remained blocked")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.waiters) != 0 {
		t.Fatal("waiter retained after cancellation")
	}
}
