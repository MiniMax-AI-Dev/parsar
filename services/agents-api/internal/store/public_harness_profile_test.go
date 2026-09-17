package store_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/google/uuid"
)

type publicHarnessProfile struct {
	artifact *nativeHarnessArtifact
	home     string
	name     string
	args     []string
	settled  bool
}

func newPublicHarnessProfile(t *testing.T, f *publicSelfHostedFixture, native, image, key string) *publicHarnessProfile {
	t.Helper()
	binary := os.Getenv("PARSAR_PUBLIC_HARNESS_ARTIFACT")
	if binary == "" {
		return nil
	}
	artifact := newNativeHarnessArtifact(t, native, binary)
	home, err := os.MkdirTemp(artifact.root, "ph-")
	if err != nil {
		t.Fatal(err)
	}
	profile := &publicHarnessProfile{artifact: artifact, home: home, name: "parsar-public-harness-" + uuid.NewString(), settled: true}
	t.Cleanup(func() {
		if profile.settled {
			if err := os.RemoveAll(home); err != nil {
				t.Error("public harness home cleanup failed", err)
			}
		}
	})
	state := filepath.Join(home, ".parsar")
	if err := os.Mkdir(state, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(home, "tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	artifact.root, artifact.configuration["root"] = state, state
	artifact.bind(t, f.environmentID, f.workspace)
	baseURL := os.Getenv("PARSAR_PLACEMENT_MODEL_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.minimax.cn/v1"
	}
	var config bytes.Buffer
	if err := toml.NewEncoder(&config).Encode(map[string]any{
		"model_provider": "public_validation",
		"model_providers": map[string]any{"public_validation": map[string]any{
			"name": "Public native validation", "base_url": baseURL,
			"env_key": "MINIMAX_VALIDATION_KEY", "wire_api": "responses",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(f.root, "native-system-config.toml")
	if err := os.WriteFile(configPath, config.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	environment := map[string]string{
		"HOME": home, "PARSAR_HOME": f.root, "PATH": "/usr/local/bin:/usr/bin:/bin", "TMPDIR": filepath.Join(home, "tmp"),
		"PARSAR_CODEX_BIN": "/opt/codex", "PARSAR_CODEX_HARNESS_BIN": "/opt/parsar-codex-harness",
		"MINIMAX_VALIDATION_KEY": key,
	}
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy"} {
		if value := os.Getenv(name); value != "" {
			environment[name] = value
		}
	}
	var envFile strings.Builder
	for name, value := range environment {
		if strings.ContainsAny(value, "\r\n") {
			t.Fatal("invalid native container environment")
		}
		fmt.Fprintf(&envFile, "%s=%s\n", name, value)
	}
	envPath := filepath.Join(f.root, "native-container.env")
	t.Cleanup(func() { _ = os.Remove(envPath) })
	if err := os.WriteFile(envPath, []byte(envFile.String()), 0600); err != nil {
		t.Fatal(err)
	}
	profile.args = []string{"run", "--name", profile.name, "--network", "host", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges",
		"--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), "--env-file", envPath, "--workdir", home,
		"--tmpfs", "/tmp:rw,nosuid,nodev,mode=1777"}
	for _, mount := range [][3]string{
		{native, "/opt/codex", ",readonly"}, {binary, "/opt/parsar-codex-harness", ",readonly"},
		{f.daemonBinary, "/opt/parsar-daemon", ",readonly"}, {configPath, "/etc/codex/config.toml", ",readonly"},
		{home, home, ""}, {filepath.Join(f.root, "parsar-daemon"), filepath.Join(f.root, "parsar-daemon"), ""},
	} {
		profile.args = append(profile.args, "--mount", "type=bind,source="+mount[0]+",target="+mount[1]+mount[2])
	}
	profile.args = append(profile.args, image, "/opt/parsar-daemon", "connect", "--profile", "execution")
	return profile
}

func (p *publicHarnessProfile) start(t *testing.T, f *publicSelfHostedFixture) *relayProcess {
	t.Helper()
	p.settled = false
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := exec.CommandContext(ctx, "docker", "rm", "-f", p.name).Run(); err != nil {
			t.Error("public harness container cleanup failed", err)
			return
		}
		p.settled = true
	})
	return startPublicNativeProcess(t, f.ctx, f.root, "daemon", os.Environ(), "docker", p.args...)
}

func (f *publicSelfHostedFixture) observeHarnessOwner(t *testing.T) {
	t.Helper()
	if f.harness == nil {
		return
	}
	a := f.harness.artifact
	owner := a.current(t)
	if a.owners[owner.PID] {
		t.Fatal("public continuation reused an earlier harness process")
	}
	a.owners[owner.PID] = true
	a.readyOwners = append(a.readyOwners, owner)
	file, err := os.OpenFile(filepath.Join(f.root, "native-starts"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := fmt.Fprintln(file, strconv.Itoa(owner.PID))
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatal("record actual public harness owner", writeErr, closeErr)
	}
}

func (f *publicSelfHostedFixture) assertHarnessReleased(t *testing.T, proof map[string]any) {
	t.Helper()
	if f.harness == nil {
		return
	}
	a := f.harness.artifact
	a.assertReleased(t, f.ctx)
	for _, owner := range a.readyOwners {
		awaitDaemonRemoteCondition(t, f.ctx, 10*time.Second, "public harness owner release", func() bool {
			_, processErr := os.Stat(filepath.Join("/proc", strconv.Itoa(owner.PID)))
			_, ipcErr := os.Stat(filepath.Dir(owner.IPCRoot))
			return os.IsNotExist(processErr) && os.IsNotExist(ipcErr)
		})
	}
	a.proof["observed_owners"] = a.readyOwners
	a.proof["container_image"] = os.Getenv("PARSAR_PLACEMENT_EXECUTOR_IMAGE")
	a.proof["configuration"] = "task-isolated native system configuration; no harness launch wrapper"
	proof["private_harness_artifact"] = a.proof
}
