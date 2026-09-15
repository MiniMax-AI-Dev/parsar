//go:build linux

package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func mcpBearerSettings(t *testing.T) (daemon, native, provider, root string) {
	t.Helper()
	names := []string{"PARSAR_MCP_BEARER_DAEMON_BIN", "PARSAR_MCP_BEARER_CODEX_BIN", "PARSAR_MCP_BEARER_MODEL_KEY_FILE", "PARSAR_MCP_BEARER_PROOF_DIR"}
	for _, name := range names {
		if os.Getenv(name) == "" {
			t.Skip("real MCP bearer acceptance requires all four explicit binary, provider-file and proof settings")
		}
		if !filepath.IsAbs(os.Getenv(name)) {
			t.Fatal("MCP bearer acceptance settings must be absolute paths")
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal("cannot resolve managed runtime home")
	}
	proof, err := filepath.EvalSymlinks(os.Getenv(names[3]))
	if err != nil {
		t.Fatal("explicit proof directory must already exist")
	}
	managed, err := filepath.EvalSymlinks(filepath.Join(home, ".parsar"))
	if err != nil || !strings.HasPrefix(proof, managed+string(os.PathSeparator)) {
		t.Fatal("proof directory must be below ~/.parsar")
	}
	root, err = os.MkdirTemp(proof, "mcp-bearer-")
	if err != nil {
		t.Fatal("cannot allocate owned proof directory")
	}
	key, err := os.ReadFile(os.Getenv(names[2]))
	if err != nil {
		t.Fatal("cannot read explicitly supplied provider key file")
	}
	provider = strings.TrimSpace(string(key))
	if provider == "" {
		t.Fatal("explicit provider key file is empty")
	}
	return os.Getenv(names[0]), os.Getenv(names[1]), provider, root
}

func mcpBearerNonce(t *testing.T) string {
	t.Helper()
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		t.Fatal("cannot generate unpredictable acceptance value")
	}
	return hex.EncodeToString(value)
}

type mcpBearerLog struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (w *mcpBearerLog) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.data.Write(p)
}
func (w *mcpBearerLog) snapshot() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return bytes.Clone(w.data.Bytes())
}

func mcpBearerStartDaemon(t *testing.T, root, daemon, native, provider, caFile, base, id, runner string, log *mcpBearerLog) {
	t.Helper()
	for _, dir := range []string{"home", "tmp", "runtime/parsar-daemon/execution"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal("cannot create owned daemon directories")
		}
	}
	auth, _ := json.Marshal(map[string]string{"server_url": base, "runtime_id": id, "runner_credential": runner})
	if err := os.WriteFile(filepath.Join(root, "runtime/parsar-daemon/execution/auth.json"), auth, 0600); err != nil {
		t.Fatal("cannot configure owned daemon identity")
	}
	wrapper := filepath.Join(root, "native-wrapper")
	script := `#!/bin/sh
set -eu
for argument in "$@"; do
  if [ "$argument" = app-server ]; then
    printf '%s %s\n' "$$" "$(awk '{print $22}' /proc/$$/stat)" >> "$PARSAR_MCP_BEARER_STARTS"
    printf '%s\0' "$@" >> "$PARSAR_MCP_BEARER_ARGV"
    break
  fi
done
exec "$PARSAR_MCP_BEARER_NATIVE" -c 'model_provider="minimax_validation"' -c 'model_providers.minimax_validation.name="MiniMax validation"' -c 'model_providers.minimax_validation.base_url="https://api.minimax.cn/v1"' -c 'model_providers.minimax_validation.env_key="MINIMAX_VALIDATION_KEY"' -c 'model_providers.minimax_validation.wire_api="responses"' "$@"
`
	if err := os.WriteFile(wrapper, []byte(script), 0700); err != nil {
		t.Fatal("cannot create owned native wrapper")
	}
	env := []string{"HOME=" + filepath.Join(root, "home"), "TMPDIR=" + filepath.Join(root, "tmp"), "PARSAR_HOME=" + filepath.Join(root, "runtime"), "PARSAR_CODEX_BIN=" + wrapper, "MINIMAX_VALIDATION_KEY=" + provider, "SSL_CERT_FILE=" + caFile, "PARSAR_MCP_BEARER_NATIVE=" + native, "PARSAR_MCP_BEARER_STARTS=" + filepath.Join(root, "native-starts"), "PARSAR_MCP_BEARER_ARGV=" + filepath.Join(root, "native-argv")}
	for _, name := range []string{"PATH", "LANG", "LC_ALL", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy"} {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	versionCtx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	version := exec.CommandContext(versionCtx, native, "--version")
	version.Env, version.Dir = env, root
	output, err := version.Output()
	if err != nil || strings.TrimSpace(string(output)) != "codex-cli 0.153.4" {
		t.Fatal("explicit native binary must be pinned Codex 0.153.4")
	}
	cmd := exec.Command(daemon, "connect", "--profile", "execution")
	cmd.Env, cmd.Dir, cmd.Stdout, cmd.Stderr = env, root, log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal("cannot start explicitly supplied daemon binary")
	}
	stopped := make(chan error, 1)
	go func() { stopped <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-stopped:
		case <-time.After(10 * time.Second):
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-stopped
		}
		// Native RPC children own separate groups. Match recorded start time before cleanup.
		_, _ = mcpBearerProcesses(root, true)
		deadline := time.Now().Add(3 * time.Second)
		for {
			_, active := mcpBearerProcesses(root, false)
			if active == 0 {
				break
			}
			if time.Now().After(deadline) {
				t.Error("owned native process did not exit during cleanup")
				break
			}
			time.Sleep(25 * time.Millisecond)
		}
	})
}

func mcpBearerProcesses(root string, kill bool) (launches, active int) {
	data, _ := os.ReadFile(filepath.Join(root, "native-starts"))
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid <= 1 {
			continue
		}
		launches++
		stat, err := os.ReadFile(filepath.Join("/proc", fields[0], "stat"))
		if err != nil {
			continue
		}
		_, tail, ok := strings.Cut(string(stat), ") ")
		state := strings.Fields(tail)
		if !ok || len(state) <= 19 || state[19] != fields[1] || state[0] == "Z" {
			continue
		}
		active++
		if group, err := syscall.Getpgid(pid); kill && err == nil && group == pid {
			_ = syscall.Kill(-group, syscall.SIGKILL)
		}
	}
	return launches, active
}

func mcpBearerReleased(t *testing.T, root string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		launches, active := mcpBearerProcesses(root, false)
		if launches > 0 && active == 0 {
			return launches
		}
		if time.Now().After(deadline) {
			t.Fatal("Done did not release the owned native process")
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func mcpBearerConfigReference(t *testing.T, root, token string) string {
	t.Helper()
	pattern := regexp.MustCompile(`(?m)^bearer_token_env_var\s*=\s*"([A-Za-z_][A-Za-z0-9_]*)"`)
	var references []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Name() != "config.toml" || !entry.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte(token)) {
			t.Error("MCP bearer persisted in native configuration")
			return nil
		}
		for _, match := range pattern.FindAllSubmatch(data, -1) {
			references = append(references, string(match[1]))
		}
		return nil
	})
	if err != nil || len(references) != 1 {
		t.Fatal("expected one native bearer environment reference")
	}
	return references[0]
}

func mcpBearerSafeWrite(t *testing.T, path string, data []byte, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if secret != "" && bytes.Contains(data, []byte(secret)) {
			t.Error("secret detected in captured acceptance artifact")
			_ = os.Remove(path)
			return
		}
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Error("cannot save safe acceptance artifact")
	}
}

func mcpBearerScanArtifacts(t *testing.T, root, token, provider string) int {
	t.Helper()
	histories := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(path, "/sessions/") && strings.HasSuffix(path, ".jsonl") {
			histories++
		}
		mcpLeak, providerPresent := bytes.Contains(data, []byte(token)), bytes.Contains(data, []byte(provider))
		if mcpLeak || providerPresent {
			if err := os.Remove(path); err != nil {
				t.Error("cannot remove secret-bearing owned artifact")
			}
			if mcpLeak {
				t.Error("injected MCP bearer persisted in an owned runtime artifact")
			}
		}
		return nil
	})
	if err != nil {
		t.Error("cannot scan owned runtime artifacts")
	}
	return histories
}

func mcpBearerBinaryHash(t *testing.T, path string) string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal("cannot read explicit acceptance binary")
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		t.Fatal("cannot fingerprint acceptance binary")
	}
	return hex.EncodeToString(digest.Sum(nil))
}
