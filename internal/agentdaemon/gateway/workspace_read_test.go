package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func workspaceReadRequest() proto.WorkspaceReadPayload {
	return proto.WorkspaceReadPayload{Handle: "prepared", EnvironmentID: "environment", Path: "file", MaxBytes: proto.WorkspaceReadMaxBytes}
}

func TestWorkspaceReadCorrelatesOneBoundedResult(t *testing.T) {
	s := NewSession(newFakeConn(), "device", "tenant", "test", nil, nil)
	defer s.Close("test")
	done := make(chan error, 1)
	data := bytes.Repeat([]byte{0, 127, 255, 3}, proto.WorkspaceReadMaxBytes/4)
	go func() {
		result, err := s.ReadWorkspaceFile(t.Context(), workspaceReadRequest())
		if err == nil && (!bytes.Equal(result.Data, data) || !result.Truncated || !result.CloseAcknowledged) {
			err = errors.New("read data or acknowledgment differs")
		}
		done <- err
	}()
	request := <-s.sendCh
	result := proto.WorkspaceReadResultPayload{Outcome: "completed", Data: data, Truncated: true, CloseAcknowledged: true}
	foreign, _ := proto.NewEnvelope(proto.TypeWorkspaceReadResult, "other-operation", result)
	s.dispatch(foreign)
	reply, _ := proto.NewEnvelope(proto.TypeWorkspaceReadResult, request.ID, result)
	encoded, err := json.Marshal(reply)
	if err != nil || int64(len(encoded)) >= ReadLimit {
		t.Fatal("result exceeds existing transport frame", err, len(encoded))
	}
	s.dispatch(reply)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if s.reg.LookupRun(request.ID) != nil {
		t.Fatal("read registered a synthetic Run")
	}
}

func TestWorkspaceReadRejectsIncompleteOrContradictoryReplies(t *testing.T) {
	for _, result := range []proto.WorkspaceReadResultPayload{
		{Outcome: "completed", Data: []byte("x")},
		{Outcome: "completed", CloseAcknowledged: true, Truncated: true},
		{Outcome: "completed", CloseAcknowledged: true, ErrorCode: "not_found"},
		{Outcome: "unknown", ErrorCode: "read_unconfirmed", Data: []byte("partial")},
		{Outcome: "rejected", ErrorCode: "native-private-secret"},
	} {
		s := NewSession(newFakeConn(), "device", "tenant", "test", nil, nil)
		done := make(chan error, 1)
		go func() { _, err := s.ReadWorkspaceFile(t.Context(), workspaceReadRequest()); done <- err }()
		request := <-s.sendCh
		reply, _ := proto.NewEnvelope(proto.TypeWorkspaceReadResult, request.ID, result)
		s.dispatch(reply)
		if err := <-done; err == nil {
			t.Fatal("invalid result accepted", result.Outcome)
		}
		s.Close("test")
	}
}

func TestWorkspaceReadObserverCancellationDoesNotSendCancelOrRetry(t *testing.T) {
	s := NewSession(newFakeConn(), "device", "tenant", "test", nil, nil)
	defer s.Close("test")
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { _, err := s.ReadWorkspaceFile(ctx, workspaceReadRequest()); done <- err }()
	request := <-s.sendCh
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	reply, _ := proto.NewEnvelope(proto.TypeWorkspaceReadResult, request.ID, proto.WorkspaceReadResultPayload{Outcome: "unknown", ErrorCode: "read_unconfirmed"})
	s.dispatch(reply)
	select {
	case extra := <-s.sendCh:
		t.Fatal("observer caused another control", extra.Type)
	default:
	}
	s.workspaceReadMu.Lock()
	count := len(s.workspaceReads)
	s.workspaceReadMu.Unlock()
	if count != 0 {
		t.Fatal("observer subscription leaked")
	}
}

func TestWorkspaceReadCapacityAndConnectionLoss(t *testing.T) {
	s := NewSession(newFakeConn(), "device", "tenant", "test", nil, nil)
	done := make(chan error, 4)
	for i := 0; i < 4; i++ {
		go func() { _, err := s.ReadWorkspaceFile(t.Context(), workspaceReadRequest()); done <- err }()
		select {
		case <-s.sendCh:
		case <-time.After(time.Second):
			t.Fatal("read not sent")
		}
	}
	if _, err := s.ReadWorkspaceFile(t.Context(), workspaceReadRequest()); err == nil {
		t.Fatal("capacity bypassed")
	}
	s.Close("connection lost")
	for i := 0; i < 4; i++ {
		if err := <-done; !errors.Is(err, ErrSessionClosed) {
			t.Fatal(err)
		}
	}
}
