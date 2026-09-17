package codex

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// privateHarness owns only this child's local IPC directory. It neither grants
// Files authority nor settles remote operations when the child exits.
type privateHarness struct {
	parent      string
	root        string
	environment string
	readMu      sync.Mutex
	reading     bool
	uncertain   bool
}

func configurePrivateHarness(cfg *JSONRPCConfig, binary string, remote *proto.RemoteEnvironment) (*privateHarness, error) {
	if binary == "" || remote == nil {
		return nil, nil
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return nil, errors.New("codex: private harness requires Linux amd64")
	}
	if !filepath.IsAbs(binary) || filepath.Clean(binary) != binary {
		return nil, errors.New("codex: private harness requires a clean absolute binary path")
	}
	info, err := os.Stat(binary)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return nil, errors.New("codex: private harness executable unavailable")
	}
	helper, err := exec.LookPath(cfg.Binary)
	if err != nil {
		return nil, errors.New("codex: stock native helper unavailable")
	}
	helper, err = filepath.Abs(helper)
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(home) {
		return nil, errors.New("codex: private harness requires an absolute home directory")
	}
	// Native admission requires canonical ~/.parsar and trusted ancestors. Keep
	// this path short independently of a potentially deep PARSAR_HOME profile.
	base, err := filepath.EvalSymlinks(filepath.Join(home, ".parsar"))
	if err != nil {
		return nil, fmt.Errorf("codex: private harness state root: %w", err)
	}
	parent, err := os.MkdirTemp(base, "ch-")
	if err != nil {
		return nil, err
	}
	harness := &privateHarness{parent: parent, root: filepath.Join(parent, "native"), environment: remote.ID}
	if len(filepath.Join(harness.root, "files.sock")) >= 104 {
		harness.cleanup()
		return nil, errors.New("codex: private harness socket path is too long")
	}
	cfg.Binary = binary
	cfg.Env = append(cfg.Env,
		"PARSAR_CODEX_HARNESS_NATIVE="+helper,
		"PARSAR_CODEX_HARNESS_DIRECTORY_HELPER="+os.Getenv("PARSAR_CODEX_DIRECTORY_HELPER"),
		"PARSAR_CODEX_HARNESS_ENVIRONMENT="+remote.ID,
		"PARSAR_CODEX_HARNESS_WORKSPACE="+remote.WorkspaceDirectory,
		"PARSAR_CODEX_HARNESS_IPC_ROOT="+harness.root)
	return harness, nil
}

func (h *privateHarness) verify() error {
	if h == nil {
		return nil
	}
	info, err := os.Lstat(filepath.Join(h.root, "files.sock"))
	if err != nil || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0600 {
		return errors.New("codex: private harness metadata endpoint unavailable")
	}
	return nil
}

// Release only after the same RPC child has been reaped, including failed
// initialization and a Close deadline. A spawn failure owns no child.
func (h *privateHarness) releaseWith(rpc *JSONRPCClient) {
	if h == nil {
		return
	}
	if rpc.cmd == nil {
		h.cleanup()
		return
	}
	go func() {
		<-rpc.Done()
		h.cleanup()
	}()
}

func (h *privateHarness) cleanup() {
	// Never recursively delete unexpected contents or another owner's directory.
	_ = os.Remove(filepath.Join(h.root, "files.sock"))
	_ = os.Remove(h.root)
	_ = os.Remove(h.parent)
}
