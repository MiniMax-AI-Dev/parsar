package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestAuthoringCLIUsesDaemonWithoutServerCredentials(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "pa-cli-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "daemon.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	t.Setenv(proto.AuthoringSocketEnv, path)
	t.Setenv("PARSAR_SERVER_URL", "")
	t.Setenv("PARSAR_RUNNER_TOKEN", "")
	file := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(file, []byte("Save this content."), 0o600); err != nil {
		t.Fatal(err)
	}
	requests := make(chan proto.AuthoringRequestPayload, 4)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			var request proto.AuthoringRequestPayload
			_ = json.NewDecoder(conn).Decode(&request)
			requests <- request
			_ = json.NewEncoder(conn).Encode(proto.AuthoringResponsePayload{Data: json.RawMessage(`{"saved":true}`)})
			_ = conn.Close()
		}
	}()
	for _, tc := range []struct {
		args            []string
		op, id, content string
	}{
		{[]string{"workspace", "context"}, proto.AuthoringContext, "", ""},
		{[]string{"skill", "create", "--file", file}, proto.AuthoringSkillCreate, "", "Save this content."},
		{[]string{"skill", "update", "--file", file, "skill-id"}, proto.AuthoringSkillUpdate, "skill-id", "Save this content."},
		{[]string{"agent", "instructions", "set", "--file", file}, proto.AuthoringPromptWrite, "", "Save this content."},
	} {
		var out bytes.Buffer
		ctx := &runContext{stdout: &out, stderr: io.Discard}
		if err := execute(ctx, tc.args); err != nil {
			t.Fatal(err)
		}
		request := <-requests
		if request.Operation != tc.op || request.CapabilityID != tc.id || request.Content != tc.content {
			t.Fatalf("wrong request: %+v", request)
		}
		if !strings.Contains(out.String(), `"saved": true`) {
			t.Fatalf("invalid JSON result: %s", &out)
		}
	}
}

func TestAuthoringCLIRequiresRunAndAbsoluteFiles(t *testing.T) {
	t.Setenv(proto.AuthoringSocketEnv, "")
	ctx := &runContext{stdout: io.Discard, stderr: io.Discard}
	for _, args := range [][]string{{"workspace", "context"}, {"skill", "create", "--file", "relative.md"}, {"agent", "instructions", "set"}} {
		if err := execute(ctx, args); err == nil {
			t.Fatalf("accepted invalid command %v", args)
		}
	}
}
