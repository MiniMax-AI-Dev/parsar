package dispatch

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/localworkspace"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

type exportSender struct{ replies chan proto.Envelope }

func (s exportSender) Send(ctx context.Context, env proto.Envelope) error {
	select {
	case s.replies <- env:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func exporterRouter(t *testing.T, program string) (*Router, exportSender, proto.WorkspaceExportPayload) {
	t.Helper()
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(dir, "export")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\n"+program+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	environment, session := uuid.NewString(), uuid.NewString()
	for key, value := range map[string]string{"PARSAR_RUNTIME_ENVIRONMENT_ID": environment, "PARSAR_RUNTIME_SESSION_ID": session, "PARSAR_RUNTIME_WORKSPACE": workspace, "PARSAR_RUNTIME_DIRECTORY_HELPER": helper, "PARSAR_RUNTIME_EXPORT_HELPER": helper, "PARSAR_RUNTIME_WRITE_HELPER": "", "PARSAR_RUNTIME_STAGING": "", "PARSAR_RUNTIME_NETWORK_ACCESS": ""} {
		t.Setenv(key, value)
	}
	binding, err := localworkspace.Load()
	if err != nil {
		t.Fatal(err)
	}
	sender := exportSender{make(chan proto.Envelope, 8)}
	r, err := New(Config{Registry: agent.NewRegistry(), Sender: sender, LocalWorkspace: binding})
	if err != nil {
		t.Fatal(err)
	}
	handle := uuid.NewString()
	r.preparations[handle] = &preparationState{workspaceReadOnly: true, environmentID: environment, owns: true, ctx: context.Background(), deadline: time.Now().Add(time.Hour), status: proto.PreparationStatusPayload{State: "ready"}}
	t.Cleanup(func() {
		r.mu.Lock()
		delete(r.preparations, handle)
		r.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := r.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	return r, sender, proto.WorkspaceExportPayload{Step: "begin", Handle: handle, EnvironmentID: environment}
}

func sendExport(t *testing.T, r *Router, id string, p proto.WorkspaceExportPayload) {
	t.Helper()
	env, err := proto.NewEnvelope(proto.TypeWorkspaceExport, id, p)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Handle(t.Context(), env); err != nil {
		t.Fatal(err)
	}
}
func readExport(t *testing.T, s exportSender) proto.WorkspaceExportResultPayload {
	t.Helper()
	select {
	case env := <-s.replies:
		var p proto.WorkspaceExportResultPayload
		if env.DecodePayload(&p) != nil {
			t.Fatal("invalid reply")
		}
		return p
	case <-time.After(3 * time.Second):
		t.Fatal("missing export reply")
	}
	return proto.WorkspaceExportResultPayload{}
}

func TestWorkspaceExportUsesExactReadPreparationAndPullsBoundedBytes(t *testing.T) {
	r, s, request := exporterRouter(t, "head -c 131089 /dev/zero")
	for _, field := range []string{"environment", "handle"} {
		bad := request
		if field == "environment" {
			bad.EnvironmentID = uuid.NewString()
		} else {
			bad.Handle = uuid.NewString()
		}
		sendExport(t, r, uuid.NewString(), bad)
		if result := readExport(t, s); result.Outcome != "rejected" {
			t.Fatal("foreign authority accepted")
		}
	}
	id := uuid.NewString()
	sendExport(t, r, id, request)
	var offset int64
	for {
		p := readExport(t, s)
		if p.Offset != offset {
			t.Fatal("wrong offset")
		}
		if p.Outcome == "completed" {
			break
		}
		if p.Outcome != "chunk" || len(p.Data) == 0 || len(p.Data) > proto.WorkspaceExportChunkBytes {
			t.Fatal("invalid chunk")
		}
		for _, b := range p.Data {
			if b != 0 {
				t.Fatal("wrong byte")
			}
		}
		offset += int64(len(p.Data))
		select {
		case <-s.replies:
			t.Fatal("export pushed unrequested data")
		default:
		}
		sendExport(t, r, id, proto.WorkspaceExportPayload{Step: "next", Offset: offset})
	}
	if offset != 131089 {
		t.Fatal("truncated export", offset)
	}
}

func TestWorkspaceExportFailureAfterBytesCannotComplete(t *testing.T) {
	r, s, request := exporterRouter(t, "printf abc; exit 1")
	id := uuid.NewString()
	sendExport(t, r, id, request)
	p := readExport(t, s)
	if p.Outcome != "chunk" || string(p.Data) != "abc" {
		t.Fatal("missing prefix", p)
	}
	sendExport(t, r, id, proto.WorkspaceExportPayload{Step: "next", Offset: 3})
	if p := readExport(t, s); p.Outcome != "failed" {
		t.Fatal("failed process appeared complete", p)
	}
}

func TestWorkspaceExportCancelUnblocksProcessAndReleasesCapacity(t *testing.T) {
	r, _, request := exporterRouter(t, "exec sleep 30")
	id := uuid.NewString()
	sendExport(t, r, id, request)
	sendExport(t, r, id, proto.WorkspaceExportPayload{Step: "cancel"})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		active := r.workspaceExport != nil
		r.mu.Unlock()
		if !active {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("export cancellation did not release capacity")
}
