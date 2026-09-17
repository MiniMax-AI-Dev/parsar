package codex

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func privateHarnessTestHome(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("private Linux amd64 artifact")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(home, ".parsar")
	if err = os.MkdirAll(base, 0700); err != nil {
		t.Fatal(err)
	}
	home, err = os.MkdirTemp(base, "ht-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
	base = filepath.Join(home, ".parsar")
	if err = os.Mkdir(base, 0700); err != nil {
		t.Fatal(err)
	}
	return base
}

func privateHarnessEnv(env []string, name string) string {
	value := ""
	for _, entry := range env {
		if strings.HasPrefix(entry, name+"=") {
			value = strings.TrimPrefix(entry, name+"=")
		}
	}
	return value
}

func awaitPrivateHarnessCleanup(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("private harness directory retained after child release")
}

func TestPrivateHarnessBindingAndDefaultSelection(t *testing.T) {
	base := privateHarnessTestHome(t)
	cfg := JSONRPCConfig{Binary: "/missing-stock", Env: []string{"unchanged=value"}}
	if h, err := configurePrivateHarness(&cfg, "relative-invalid", nil); h != nil || err != nil || cfg.Binary != "/missing-stock" || len(cfg.Env) != 1 {
		t.Fatal("nonremote default changed")
	}
	remote := &proto.RemoteEnvironment{ID: "fixture", WorkspaceDirectory: "/remote"}
	if h, err := configurePrivateHarness(&cfg, "", remote); h != nil || err != nil || len(cfg.Env) != 1 {
		t.Fatal("stock remote default changed")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"relative", filepath.Join(base, "missing"), base} {
		if _, err := configurePrivateHarness(&cfg, path, remote); err == nil {
			t.Fatal("invalid artifact admitted", path)
		}
	}
	if _, err := configurePrivateHarness(&cfg, binary, remote); err == nil {
		t.Fatal("missing helper admitted")
	}
	cfg.Binary = binary
	t.Setenv("PARSAR_CODEX_DIRECTORY_HELPER", "/trusted/directory-helper")
	cfg.Env = append(cfg.Env, "PARSAR_CODEX_HARNESS_DIRECTORY_HELPER=/caller/override", "PARSAR_CODEX_HARNESS_ENVIRONMENT=wrong", "PARSAR_CODEX_HARNESS_IPC_ROOT=/wrong")
	h, err := configurePrivateHarness(&cfg, binary, remote)
	if err != nil {
		t.Fatal(err)
	}
	defer h.cleanup()
	if privateHarnessEnv(cfg.Env, "PARSAR_CODEX_HARNESS_DIRECTORY_HELPER") != "/trusted/directory-helper" {
		t.Fatal("directory helper was not selected by operator")
	}
	if cfg.Binary != binary || privateHarnessEnv(cfg.Env, "PARSAR_CODEX_HARNESS_NATIVE") != binary || privateHarnessEnv(cfg.Env, "PARSAR_CODEX_HARNESS_ENVIRONMENT") != remote.ID || privateHarnessEnv(cfg.Env, "PARSAR_CODEX_HARNESS_WORKSPACE") != remote.WorkspaceDirectory || privateHarnessEnv(cfg.Env, "PARSAR_CODEX_HARNESS_IPC_ROOT") != h.root {
		t.Fatal("private binding not derived from operator and request")
	}
	if filepath.Dir(h.parent) != base {
		t.Fatal("private IPC escaped home")
	}
	if _, err := os.Lstat(h.root); !os.IsNotExist(err) {
		t.Fatal("native socket root already exists")
	}
	if info, err := os.Stat(h.parent); err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("IPC parent is not private")
	}
	if h.verify() == nil {
		t.Fatal("missing endpoint accepted")
	}
}

func TestPrivateHarnessPreparationTransferAndRelease(t *testing.T) {
	for _, start := range []bool{false, true} {
		t.Run(map[bool]string{false: "unused", true: "transferred"}[start], func(t *testing.T) {
			privateHarnessTestHome(t)
			req, cfg, root := preparationFixture(t)
			cfg.harnessBinary = cfg.codexBinary
			t.Setenv("PARSAR_PRIVATE_HARNESS_FAKE", "1")
			owner, cancel := context.WithCancel(t.Context())
			defer cancel()
			p, err := newPreparation(owner, req, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Cancel(context.Background())
			ipc := privateHarnessEnv(p.session.rpc.cfg.Env, "PARSAR_CODEX_HARNESS_IPC_ROOT")
			if ipc == "" {
				t.Fatal("adapter did not configure the artifact")
			}
			assertPreparationOnly(t, root)
			if start {
				out := make(chan proto.Envelope, 32)
				if _, err = p.Start(t.Context(), "run", "hello", out); err != nil {
					t.Fatal(err)
				}
				waitPreparationMethod(t, root, "turn/start")
				if _, err = p.Start(t.Context(), "second", "hello", out); err == nil {
					t.Fatal("second Start accepted")
				}
				if err = p.Close(); err != nil {
					t.Fatal(err)
				}
				if _, err = os.Lstat(ipc); err != nil {
					t.Fatal("transfer lost IPC before Session release")
				}
			}
			if err = p.Cancel(t.Context()); err != nil {
				t.Fatal(err)
			}
			awaitPrivateHarnessCleanup(t, filepath.Dir(ipc))
			waitPreparedRelease(t, p, root)
		})
	}
}

func TestPrivateHarnessRejectsOrdinaryBinaryBeforeStart(t *testing.T) {
	base := privateHarnessTestHome(t)
	req, cfg, root := preparationFixture(t)
	cfg.harnessBinary = cfg.codexBinary
	t.Setenv("PARSAR_PRIVATE_HARNESS_FAKE", "")
	if p, err := newPreparation(t.Context(), req, cfg); err == nil {
		_ = p.Close()
		t.Fatal("ordinary binary admitted as integrated artifact")
	}
	assertPreparationOnly(t, root)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		entries, err := os.ReadDir(base)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) == 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("failed initialization retained private IPC allocation")
}

func TestPrivateHarnessCleanupWaitsForRPCSettlement(t *testing.T) {
	base := privateHarnessTestHome(t)
	for _, spawned := range []bool{false, true} {
		parent, err := os.MkdirTemp(base, "ch-")
		if err != nil {
			t.Fatal(err)
		}
		h := &privateHarness{parent: parent, root: filepath.Join(parent, "native")}
		rpc := NewJSONRPCClient(JSONRPCConfig{})
		if spawned {
			rpc.cmd = exec.Command("controlled-unreaped-owner")
		}
		h.releaseWith(rpc)
		if spawned {
			if _, err = os.Stat(parent); err != nil {
				t.Fatal("allocation removed before child settlement")
			}
			close(rpc.doneCh)
		}
		awaitPrivateHarnessCleanup(t, parent)
	}
}

// Only the controlled native fixture uses this socket. Real artifact tests run
// the pinned executable and independently inspect actual remote metadata.
func fakePrivateHarnessEndpoint() {
	if os.Getenv("PARSAR_PRIVATE_HARNESS_FAKE") != "1" {
		return
	}
	root := os.Getenv("PARSAR_CODEX_HARNESS_IPC_ROOT")
	if root == "" || os.Mkdir(root, 0700) != nil {
		os.Exit(7)
	}
	path := filepath.Join(root, "files.sock")
	listener, err := net.Listen("unix", path)
	if err != nil || os.Chmod(path, 0600) != nil {
		os.Exit(7)
	}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
}
