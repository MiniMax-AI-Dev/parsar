package gateway

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func exportReply(t *testing.T, s *Session, id string, result proto.WorkspaceExportResultPayload) {
	t.Helper()
	env, err := proto.NewEnvelope(proto.TypeWorkspaceExportResult, id, result)
	if err != nil {
		t.Fatal(err)
	}
	s.dispatch(env)
}

func TestWorkspaceExportPullsOnlyAfterConsumedAndRequiresCompletion(t *testing.T) {
	s := NewSession(newFakeConn(), "device", "tenant", "test", nil, nil)
	defer s.Close("test")
	consumed, resume := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	body := bytes.Repeat([]byte{0, 255, 7}, 100)
	go func() {
		done <- s.ExportWorkspaceOutputs(t.Context(), proto.WorkspaceExportPayload{Handle: "prepared", EnvironmentID: "env"}, func(r io.Reader) error {
			got := make([]byte, len(body))
			if _, err := io.ReadFull(r, got); err != nil {
				return err
			}
			if !bytes.Equal(got, body) {
				return errors.New("bytes differ")
			}
			close(consumed)
			<-resume
			return nil
		})
	}()
	first := <-s.sendCh
	exportReply(t, s, "foreign", proto.WorkspaceExportResultPayload{Outcome: "completed"})
	exportReply(t, s, first.ID, proto.WorkspaceExportResultPayload{Outcome: "chunk", Data: body})
	<-consumed
	select {
	case env := <-s.sendCh:
		t.Fatalf("premature next chunk: %s", env.Type)
	default:
	}
	close(resume)
	next := <-s.sendCh
	var request proto.WorkspaceExportPayload
	if next.DecodePayload(&request) != nil || request.Step != "next" || request.Offset != int64(len(body)) {
		t.Fatal("bad continuation", request)
	}
	select {
	case err := <-done:
		t.Fatal("succeeded before native completion", err)
	default:
	}
	exportReply(t, s, next.ID, proto.WorkspaceExportResultPayload{Outcome: "completed", Offset: int64(len(body))})
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceExportRejectsInvalidChunksAndNativeFailure(t *testing.T) {
	for _, result := range []proto.WorkspaceExportResultPayload{
		{Outcome: "chunk", Offset: 1, Data: []byte("x")},
		{Outcome: "chunk", Data: make([]byte, proto.WorkspaceExportChunkBytes+1)},
		{Outcome: "chunk"},
		{Outcome: "completed", Data: []byte("x")},
		{Outcome: "failed", ErrorCode: "export_failed"},
	} {
		s := NewSession(newFakeConn(), "device", "tenant", "test", nil, nil)
		done := make(chan error, 1)
		go func() {
			done <- s.ExportWorkspaceOutputs(t.Context(), proto.WorkspaceExportPayload{Handle: "prepared", EnvironmentID: "env"}, func(r io.Reader) error { _, err := io.Copy(io.Discard, r); return err })
		}()
		first := <-s.sendCh
		exportReply(t, s, first.ID, result)
		if err := <-done; err == nil {
			t.Fatal("invalid export accepted", result.Outcome)
		}
		cancel := <-s.sendCh
		var request proto.WorkspaceExportPayload
		if cancel.DecodePayload(&request) != nil || request.Step != "cancel" {
			t.Fatal("missing cleanup request")
		}
		s.Close("test")
	}
}

func TestWorkspaceExportDisconnectAndCancellationSettleRead(t *testing.T) {
	for _, disconnect := range []bool{false, true} {
		s := NewSession(newFakeConn(), "device", "tenant", "test", nil, nil)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() {
			done <- s.ExportWorkspaceOutputs(ctx, proto.WorkspaceExportPayload{Handle: "prepared", EnvironmentID: "env"}, func(r io.Reader) error { _, err := io.Copy(io.Discard, r); return err })
		}()
		<-s.sendCh
		if disconnect {
			s.Close("lost")
		} else {
			cancel()
		}
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("interrupted export succeeded")
			}
		case <-time.After(time.Second):
			t.Fatal("read did not settle")
		}
		cancel()
		s.Close("test")
	}
}
