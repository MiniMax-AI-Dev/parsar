//go:build unix

package claudesdk

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestWorkspaceLaunchAndReadinessExcludeParentEnvironment(t *testing.T) {
	config := workspaceFixture(t)
	t.Setenv("PARSAR_PARENT_SECRET", "must-not-inherit")
	t.Setenv("ANTHROPIC_API_KEY", "unselected")
	// An owned process fixture verifies both real subprocess launch paths; it is
	// not a native sandbox or provider acceptance test.
	script := `#!/bin/sh
test -z "${PARSAR_PARENT_SECRET+x}" || exit 21
test -z "${ANTHROPIC_API_KEY+x}" || exit 22
test "$ANTHROPIC_AUTH_TOKEN" = selected-provider-fixture || exit 23
test "$TMPDIR" != "$CLAUDE_CONFIG_DIR/tmp" || exit 24
case "$1" in
  */runtime_check.js)
    printf '%s\n' '{"type":"runtime_ready","protocol":1,"node":"fixture","sdk":"fixture","mcp":"fixture","native":"fixture","features":["workspace_tools"]}' ;;
  *)
    IFS= read -r request
    printf '%s\n' '{"type":"result","session_id":"native","text":"completed"}' ;;
esac
`
	if err := os.WriteFile(config.Node, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	info, err := CheckRuntime(t.Context(), config)
	if err != nil || !info.supportsWorkspace() {
		t.Fatal("readiness did not receive replacement environment", err)
	}
	out := make(chan proto.Envelope, 8)
	s, err := NewFactory(config)(t.Context(), workspaceRequest(), out)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Cancel(t.Context())
	done := 0
	for event := range out {
		if event.Type == proto.TypeError {
			t.Fatal("execution fixture rejected replacement environment")
		}
		if event.Type == proto.TypeDone {
			done++
		}
	}
	if done != 1 {
		t.Fatal("expected one settled completion")
	}
	// Feature checking must reject an older bridge without starting execution.
	script = strings.ReplaceAll(script, `"features":["workspace_tools"]`, `"features":[]`)
	script = strings.ReplaceAll(script, "IFS= read -r request", "touch '"+filepath.Join(config.StateDir, "unexpected-start")+"'")
	if err := os.WriteFile(config.Node, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFactory(config)(t.Context(), workspaceRequest(), out); err == nil {
		t.Fatal("old packaged bridge accepted workspace execution")
	}
	if _, err := os.Stat(filepath.Join(config.StateDir, "unexpected-start")); !os.IsNotExist(err) {
		t.Fatal("old packaged bridge started execution")
	}
}

func TestWorkspaceRejectsMutableRuntimeAliasesBeforeReadiness(t *testing.T) {
	for _, name := range []string{"node", "entrypoint", "scratch-entrypoint", "companion", "dependencies"} {
		t.Run(name, func(t *testing.T) {
			config := workspaceFixture(t)
			marker := filepath.Join(config.Workspace.Directory, "unexpected-launch")
			if err := os.WriteFile(config.Node, []byte("#!/bin/sh\nprintf started > '"+marker+"'\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			link, target := "", ""
			switch name {
			case "node":
				link, target = filepath.Join(config.Workspace.Directory, "node"), config.Node
				config.Node = link
			case "entrypoint", "scratch-entrypoint":
				dir := config.Workspace.Directory
				if name == "scratch-entrypoint" {
					dir = config.Workspace.ScratchDir
				}
				link, target = filepath.Join(dir, "main.js"), config.Entrypoint
				config.Entrypoint = link
				if err := os.WriteFile(filepath.Join(dir, "runtime_check.js"), []byte("untrusted companion"), 0o700); err != nil {
					t.Fatal(err)
				}
			case "companion":
				link, target = filepath.Join(filepath.Dir(config.Entrypoint), "runtime_check.js"), filepath.Join(config.Workspace.Directory, "companion.js")
				if err := os.Remove(link); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(target, []byte("untrusted companion"), 0o700); err != nil {
					t.Fatal(err)
				}
			case "dependencies":
				link, target = filepath.Join(config.Workspace.Directory, "deps"), config.Workspace.DependencyPath
				config.Workspace.DependencyPath = link
			}
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			if _, err := CheckRuntime(t.Context(), config); err == nil {
				t.Fatal("mutable runtime alias passed readiness")
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("invalid binding launched a credential-bearing child")
			}
		})
	}
}

func TestWorkspaceUsesCanonicalDependencyPaths(t *testing.T) {
	config := workspaceFixture(t)
	canonical := config.Workspace.DependencyPath
	alias := filepath.Join(filepath.Dir(config.StateDir), "dependency-alias")
	if err := os.Symlink(canonical, alias); err != nil {
		t.Fatal(err)
	}
	config.Workspace.DependencyPath = alias
	start, env, err := prepare(config, workspaceRequest())
	if err != nil {
		t.Fatal(err)
	}
	if start.Workspace.DependencyPath != canonical || env[0] != "PATH="+canonical {
		t.Fatal("trusted launch retained a mutable dependency alias")
	}
}
