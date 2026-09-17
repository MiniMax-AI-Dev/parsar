package dispatch_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/dispatch"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/localworkspace"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

func localWriterRouter(t *testing.T, response string) (*dispatch.Router, *recSender, proto.WorkspaceWritePayload, string) {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspace, staging := filepath.Join(parent, "workspace"), filepath.Join(parent, "staging")
	for _, p := range []string{workspace, staging} {
		if err := os.Mkdir(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	helperRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(helperRoot, "helper")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\ncat >/dev/null\ntouch \"$1/invoked\"\nprintf '%s' '"+response+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	environment, session := uuid.NewString(), uuid.NewString()
	for k, v := range map[string]string{"PARSAR_RUNTIME_ENVIRONMENT_ID": environment, "PARSAR_RUNTIME_SESSION_ID": session, "PARSAR_RUNTIME_WORKSPACE": workspace, "PARSAR_RUNTIME_DIRECTORY_HELPER": helper, "PARSAR_RUNTIME_WRITE_HELPER": helper, "PARSAR_RUNTIME_STAGING": staging} {
		t.Setenv(k, v)
	}
	binding, err := localworkspace.Load()
	if err != nil {
		t.Fatal(err)
	}
	sender := &recSender{}
	r, err := dispatch.New(dispatch.Config{Registry: agent.NewRegistry(), Sender: sender, LocalWorkspace: binding})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = r.Shutdown(ctx)
	})
	digest := sha256.Sum256([]byte("abc"))
	return r, sender, proto.WorkspaceWritePayload{Step: "begin", EnvironmentID: environment, SessionID: session, Path: "file", SizeBytes: 3, SHA256: hex.EncodeToString(digest[:])}, workspace
}

func waitWorkspaceWrite(t *testing.T, sender *recSender, id, outcome string) proto.WorkspaceWriteResultPayload {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, env := range sender.snapshot() {
			if env.ID != id || env.Type != proto.TypeWorkspaceWriteResult {
				continue
			}
			var result proto.WorkspaceWriteResultPayload
			if env.DecodePayload(&result) != nil {
				t.Fatal("bad write result")
			}
			if result.Outcome == outcome {
				return result
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("write result missing", id, outcome)
	return proto.WorkspaceWriteResultPayload{}
}

func TestLocalUploadRequiresExactScopeAndCompleteBody(t *testing.T) {
	r, sender, request, workspace := localWriterRouter(t, `{"version":1,"outcome":"completed","size_bytes":3}`)
	for _, field := range []string{"environment", "session"} {
		bad := request
		if field == "environment" {
			bad.EnvironmentID = uuid.NewString()
		} else {
			bad.SessionID = uuid.NewString()
		}
		id := uuid.NewString()
		if err := r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceWrite, id, bad)); err != nil {
			t.Fatal(err)
		}
		waitWorkspaceWrite(t, sender, id, "rejected")
	}
	id := uuid.NewString()
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceWrite, id, request)); err != nil {
		t.Fatal(err)
	}
	waitWorkspaceWrite(t, sender, id, "ready")
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceWrite, id, request)); err == nil {
		t.Fatal("duplicate transfer accepted")
	}
	for offset, part := range []string{"a", "bc"} {
		if err := r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceWrite, id, proto.WorkspaceWritePayload{Step: "chunk", Offset: offset, Data: []byte(part)})); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(workspace, "invoked")); !os.IsNotExist(err) {
		t.Fatal("helper started before commit")
	}
	if err := r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceWrite, id, proto.WorkspaceWritePayload{Step: "commit"})); err != nil {
		t.Fatal(err)
	}
	if got := waitWorkspaceWrite(t, sender, id, "completed"); got.SizeBytes != 3 {
		t.Fatal(got)
	}
	if _, err := os.Stat(filepath.Join(workspace, "invoked")); err != nil {
		t.Fatal("helper not called", err)
	}
}

func TestLocalUploadRejectsReorderedOrCorruptBodiesWithoutMutation(t *testing.T) {
	for _, mode := range []string{"offset", "digest", "short"} {
		t.Run(mode, func(t *testing.T) {
			r, sender, request, workspace := localWriterRouter(t, `{"version":1,"outcome":"completed","size_bytes":3}`)
			id := uuid.NewString()
			_ = r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceWrite, id, request))
			chunk := proto.WorkspaceWritePayload{Step: "chunk", Data: []byte("abc")}
			switch mode {
			case "offset":
				chunk.Offset = 1
			case "digest":
				chunk.Data = []byte("bad")
			case "short":
				chunk.Data = []byte("a")
			}
			_ = r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceWrite, id, chunk))
			_ = r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceWrite, id, proto.WorkspaceWritePayload{Step: "commit"}))
			waitWorkspaceWrite(t, sender, id, "rejected")
			if _, err := os.Stat(filepath.Join(workspace, "invoked")); !os.IsNotExist(err) {
				t.Fatal("bad transfer invoked helper")
			}
		})
	}
}

func TestLocalUploadUnknownRetainsOwner(t *testing.T) {
	r, sender, request, _ := localWriterRouter(t, `{"version":1,"outcome":"unknown","error":"write_failed"}`)
	id := uuid.NewString()
	for _, p := range []proto.WorkspaceWritePayload{request, {Step: "chunk", Data: []byte("abc")}, {Step: "commit"}} {
		if err := r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceWrite, id, p)); err != nil {
			t.Fatal(err)
		}
	}
	waitWorkspaceWrite(t, sender, id, "unknown")
	next := uuid.NewString()
	_ = r.Handle(t.Context(), mustEnv(t, proto.TypeWorkspaceWrite, next, request))
	if got := waitWorkspaceWrite(t, sender, next, "rejected"); got.ErrorCode != "write_capacity" {
		t.Fatal(got)
	}
	if err := r.Shutdown(t.Context()); err == nil {
		t.Fatal("shutdown declared uncertain mutation settled")
	}
}
