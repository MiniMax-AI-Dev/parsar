package dispatch_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type directoryTestReader struct{ calls atomic.Int32 }

func (r *directoryTestReader) ListWorkspaceDirectory(_ context.Context, path string, limit int) (agent.WorkspaceDirectoryResult, error) {
	if path != "" || limit != 2 {
		return agent.WorkspaceDirectoryResult{}, agent.ErrWorkspaceReadInvalid
	}
	r.calls.Add(1)
	size := int64(3)
	return agent.WorkspaceDirectoryResult{Entries: []agent.WorkspaceDirectoryEntry{{Name: "file", Kind: "file", SizeBytes: &size}}, Truncated: true}, nil
}

type directoryPreparation struct {
	*controlledPreparation
	*directoryTestReader
}
type directorySession struct {
	*fakeSession
	*directoryTestReader
}

func TestWorkspaceDirectoryRetainsEnvironmentAndTransferredOwner(t *testing.T) {
	sender := &recSender{}
	reader := &directoryTestReader{}
	p := &directoryPreparation{&controlledPreparation{closed: make(chan struct{})}, reader}
	p.start = func(ctx context.Context, _ string, _ string, out chan<- proto.Envelope) (agent.Session, error) {
		return &directorySession{&fakeSession{out: out, ctx: ctx, closeOutOnCancel: true}, reader}, nil
	}
	r := preparationRouter(t, sender, time.Minute, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) { return p, nil })
	_ = r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "prepare", preparationRequest()))
	ready := waitPreparationStatus(t, sender, "prepare", "ready", "")
	request := proto.WorkspaceReadPayload{Operation: "directory", Handle: ready.Handle, EnvironmentID: "environment", MaxEntries: 2}
	for _, phase := range []string{"idle", "active"} {
		_ = r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceRead, phase, request))
		result := waitWorkspaceRead(t, sender, phase)
		if result.Outcome != "completed" || !result.CloseAcknowledged || result.Directory == nil || !result.Directory.Truncated || len(result.Directory.Entries) != 1 || len(result.Data) != 0 {
			t.Fatal(result)
		}
		bad := request
		bad.EnvironmentID = "another-environment"
		_ = r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceRead, phase+"-foreign", bad))
		if got := waitWorkspaceRead(t, sender, phase+"-foreign"); got.ErrorCode != "resource_unavailable" {
			t.Fatal(got)
		}
		if phase == "idle" {
			_ = r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionStart, "prepare", proto.ExecutionStartPayload{Handle: ready.Handle, RunID: "run", Prompt: "start"}))
			waitPreparationStatus(t, sender, "prepare", "started", "")
			_ = r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceRead, "stale", request))
			if got := waitWorkspaceRead(t, sender, "stale"); got.Outcome != "rejected" {
				t.Fatal(got)
			}
			request.Handle, request.RunID = "", "run"
		}
	}
	for index, bad := range []proto.WorkspaceReadPayload{
		{Operation: "directory", RunID: "run", EnvironmentID: "environment", MaxEntries: 2, MaxBytes: 1},
		{Operation: "directory", RunID: "run", EnvironmentID: "environment", MaxEntries: proto.WorkspaceDirectoryMaxEntries + 1},
		{Operation: "directory", Handle: ready.Handle, RunID: "run", EnvironmentID: "environment", MaxEntries: 2},
		{Operation: "recursive", RunID: "run", EnvironmentID: "environment", MaxEntries: 2},
	} {
		id := fmt.Sprintf("invalid-%d", index)
		_ = r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceRead, id, bad))
		if got := waitWorkspaceRead(t, sender, id); got.Outcome != "rejected" || got.ErrorCode != "invalid_request" || got.Directory != nil {
			t.Fatal("malformed directory control reached a resource", got)
		}
	}
	if reader.calls.Load() != 2 {
		t.Fatal("wrong owner was observed", reader.calls.Load())
	}
}
