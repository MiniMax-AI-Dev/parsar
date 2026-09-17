package dispatch_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/dispatch"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/localworkspace"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

func TestLocalDirectoryPreparationNeedsNoHarnessAndRejectsOtherOwners(t *testing.T) {
	workspace, helper := t.TempDir(), filepath.Join(t.TempDir(), "directory")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nprintf '%s' '{\"version\":1,\"directory\":{\"entries\":[],\"truncated\":false}}'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	environment, session := uuid.NewString(), uuid.NewString()
	binding, err := localworkspace.New(environment, session, workspace, helper)
	if err != nil {
		t.Fatal(err)
	}
	var harnessCalls atomic.Int32
	reg := agent.NewRegistry()
	reg.RegisterKind(proto.SupportedAgentKind{Kind: "native", Available: true, Capabilities: proto.AgentKindCapabilities{LocalEnvironment: true}}, func(context.Context, proto.PromptRequestPayload, chan<- proto.Envelope) (agent.Session, error) {
		harnessCalls.Add(1)
		return nil, errors.New("must not start a model")
	})
	reg.RegisterPreparation("native", true, func(context.Context, proto.PromptRequestPayload) (agent.Prepared, error) {
		harnessCalls.Add(1)
		return nil, errors.New("must not prepare a harness")
	})
	sender := &recSender{}
	r, err := dispatch.New(dispatch.Config{Registry: reg, Sender: sender, LocalWorkspace: binding})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Shutdown(context.Background()) })
	request := proto.PromptRequestPayload{AgentKind: "native", LocalEnvironment: &proto.LocalEnvironment{ID: environment}, AgentStateKey: "agents-api-" + session, StrictResume: true, ReleaseOnCompletion: true, WorkspaceReadOnly: true}
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "idle", proto.ExecutionPreparePayload{Configuration: request})); err != nil {
		t.Fatal(err)
	}
	ready := waitPreparationStatus(t, sender, "idle", "ready", "")
	read := proto.WorkspaceReadPayload{EnvironmentID: environment, Handle: ready.Handle, Operation: "directory", MaxEntries: 10}
	_ = r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceRead, "list", read))
	if got := waitWorkspaceRead(t, sender, "list"); got.Outcome != "completed" || got.Directory == nil {
		t.Fatal("idle directory unavailable", got)
	}
	read.EnvironmentID = uuid.NewString()
	_ = r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceRead, "foreign", read))
	if got := waitWorkspaceRead(t, sender, "foreign"); got.Outcome != "rejected" {
		t.Fatal("foreign directory accepted", got)
	}
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionStart, "idle", proto.ExecutionStartPayload{Handle: ready.Handle, RunID: "forbidden", Prompt: "work"})); err == nil {
		t.Fatal("read preparation admitted execution")
	}
	bad := request
	bad.AgentStateKey = "agents-api-" + uuid.NewString()
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionPrepare, "wrong-session", proto.ExecutionPreparePayload{Configuration: bad})); err == nil {
		t.Fatal("wrong Session accepted")
	}
	_ = r.Handle(t.Context(), mustEnv(t, proto.TypeExecutionRelease, "idle", proto.ExecutionReleasePayload{Handle: ready.Handle}))
	waitPreparationStatus(t, sender, "idle", "released", "")
	if harnessCalls.Load() != 0 || r.ActiveRuns() != 0 {
		t.Fatal("read-only operation reached native execution")
	}
}
