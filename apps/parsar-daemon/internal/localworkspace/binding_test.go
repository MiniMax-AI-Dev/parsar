package localworkspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

func testBinding(t *testing.T) (*Binding, proto.PromptRequestPayload) {
	t.Helper()
	root := t.TempDir()
	helper := filepath.Join(t.TempDir(), "helper")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	environment, session := uuid.NewString(), uuid.NewString()
	b, err := New(environment, session, root, helper)
	if err != nil {
		t.Fatal(err)
	}
	return b, proto.PromptRequestPayload{LocalEnvironment: &proto.LocalEnvironment{ID: environment}, AgentStateKey: "agents-api-" + session, StrictResume: true, ReleaseOnCompletion: true}
}

func TestBindingRejectsScopeAndPathOverrides(t *testing.T) {
	b, valid := testBinding(t)
	configured, err := b.Configure(valid)
	if err != nil || configured.WorkDir != b.workspace {
		t.Fatalf("frozen cwd: %+v %v", configured, err)
	}
	for name, mutate := range map[string]func(*proto.PromptRequestPayload){
		"missing reference": func(r *proto.PromptRequestPayload) { r.LocalEnvironment = nil },
		"other Environment": func(r *proto.PromptRequestPayload) {
			r.LocalEnvironment = &proto.LocalEnvironment{ID: uuid.NewString()}
		},
		"other Session":     func(r *proto.PromptRequestPayload) { r.AgentStateKey = "agents-api-" + uuid.NewString() },
		"path override":     func(r *proto.PromptRequestPayload) { r.WorkDir = b.workspace },
		"remote":            func(r *proto.PromptRequestPayload) { r.RemoteEnvironment = &proto.RemoteEnvironment{ID: "other"} },
		"none":              func(r *proto.PromptRequestPayload) { r.DisableExecutionEnvironment = true },
		"product authoring": func(r *proto.PromptRequestPayload) { r.WorkspaceAuthoring = true },
		"non-strict resume": func(r *proto.PromptRequestPayload) { r.StrictResume = false },
	} {
		t.Run(name, func(t *testing.T) {
			r := valid
			mutate(&r)
			if _, err := b.Configure(r); err == nil {
				t.Fatal("unsafe request accepted")
			}
		})
	}
	if _, err := (*Binding)(nil).Configure(valid); err == nil {
		t.Fatal("unbound Runtime accepted a local Environment")
	}
	if _, err := (*Binding)(nil).Configure(proto.PromptRequestPayload{}); err != nil {
		t.Fatal("ordinary unbound behavior changed", err)
	}
}

func TestLocalHelperCannotInheritCredentials(t *testing.T) {
	b, _ := testBinding(t)
	t.Setenv("PARSAR_PRIVATE_CREDENTIAL", "synthetic-secret")
	script := "#!/bin/sh\n[ -z \"$PARSAR_PRIVATE_CREDENTIAL\" ] || exit 13\nprintf '%s' '{\"version\":1,\"directory\":{\"entries\":[],\"truncated\":false}}'\n"
	if err := os.WriteFile(b.helper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := b.ListWorkspaceDirectory(t.Context(), "", 2)
	if err != nil || got.Entries == nil || len(got.Entries) != 0 || got.Truncated {
		t.Fatalf("read-only helper: %+v %v", got, err)
	}
	for _, path := range []string{"/etc", "..", "a/../b", "a//b", ".", "a\\b"} {
		if _, err := b.ListWorkspaceDirectory(t.Context(), path, 2); err == nil {
			t.Fatalf("invalid path accepted: %q", path)
		}
	}
}

func TestDirectoryRejectsMalformedOrIncompleteResponses(t *testing.T) {
	for _, frame := range []string{
		`{"version":2,"directory":{"entries":[],"truncated":false}}`,
		`{"version":1,"directory":{"entries":[]}}`,
		`{"version":1,"directory":{"entries":null,"truncated":false}}`,
		`{"version":1,"directory":{"entries":[{"name":"../secret","kind":"file","size_bytes":1}],"truncated":false}}`,
		`{"version":1,"error":"unknown"}`,
		`{"version":1,"directory":{"entries":[],"truncated":false}} {}`,
	} {
		if _, err := decodeDirectory([]byte(frame), 2); err == nil {
			t.Fatal("unconfirmed response accepted", frame)
		}
	}
}
