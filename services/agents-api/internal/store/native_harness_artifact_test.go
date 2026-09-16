package store_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNativeDaemonHarnessArtifact(t *testing.T) {
	artifact := os.Getenv("PARSAR_CODEX_HARNESS_ARTIFACT")
	if artifact == "" {
		t.Skip("explicit final private harness artifact required")
	}
	testNativeDaemonRemoteEnvironmentWithArtifact(t, true, artifact)
}

type nativeHarnessArtifact struct {
	root          string
	wrapper       string
	environment   string
	configuration map[string]string
	proof         map[string]any
	owners        map[int]bool
	readyOwners   []nativeHarnessOwner
}

type nativeHarnessOwner struct {
	PID            int    `json:"pid"`
	IPCRoot        string `json:"ipc_root"`
	ArtifactSHA256 string `json:"artifact_sha256"`
}

func newNativeHarnessArtifact(t *testing.T, native, artifact string) *nativeHarnessArtifact {
	t.Helper()
	if !filepath.IsAbs(native) || !filepath.IsAbs(artifact) {
		t.Fatal("artifact and native helper paths must be absolute")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	state, err := filepath.EvalSymlinks(filepath.Join(home, ".parsar"))
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(state, "hf-")
	if err != nil {
		t.Fatal(err)
	}
	configuration := map[string]string{"native": native, "artifact": artifact, "root": root}
	configuration["artifact_sha256"] = nativeHarnessFileHash(t, artifact)
	path, _ := json.Marshal(filepath.Join(root, "binding.json"))
	nativePath, _ := json.Marshal(native)
	wrapper := filepath.Join(root, "codex")
	script := fmt.Sprintf(nativeHarnessWrapper, nativePath, path)
	if err := os.WriteFile(wrapper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	proof := map[string]any{
		"artifact": artifact, "artifact_sha256": configuration["artifact_sha256"],
		"native_helper": native, "native_helper_sha256": nativeHarnessFileHash(t, native),
		"wrapper": wrapper, "wrapper_sha256": nativeHarnessFileHash(t, wrapper),
		"execution_caller":      "existing daemon Go JSONRPCClient through private operator fixture wrapper",
		"preflight":             "stock helper --version; app-server executes the final private artifact",
		"metadata_observations": []map[string]any{},
	}
	return &nativeHarnessArtifact{root: root, wrapper: wrapper, configuration: configuration, proof: proof, owners: make(map[int]bool)}
}

func (a *nativeHarnessArtifact) bind(t *testing.T, environment, workspace string) {
	t.Helper()
	a.environment = environment
	a.configuration["environment"] = environment
	a.configuration["workspace"] = workspace
	data, err := json.Marshal(a.configuration)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(a.root, "binding.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func (a *nativeHarnessArtifact) current(t *testing.T) nativeHarnessOwner {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(a.root, "owners.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var owner nativeHarnessOwner
	if json.Unmarshal([]byte(lines[len(lines)-1]), &owner) != nil || owner.PID <= 1 || owner.ArtifactSHA256 != a.configuration["artifact_sha256"] || filepath.Dir(owner.IPCRoot) != a.root {
		t.Fatal("invalid private artifact owner record")
	}
	return owner
}

func (a *nativeHarnessArtifact) observeReady(t *testing.T, ctx context.Context, phase, local string) {
	t.Helper()
	owner := a.current(t)
	if a.owners[owner.PID] {
		t.Fatal("fresh preparation reused an earlier harness process")
	}
	a.owners[owner.PID] = true
	a.readyOwners = append(a.readyOwners, owner)
	a.proof["distinct_ready_owners"] = len(a.owners)
	a.metadata(t, ctx, owner, "ready_"+phase, "placement.sh", local)
	if phase == "first" {
		a.expectError(t, ctx, owner, a.environment, "retained.txt", "not_found")
	} else {
		a.metadata(t, ctx, owner, "ready_"+phase, "retained.txt", local)
	}
	a.expectError(t, ctx, owner, uuid.NewString(), "placement.sh", "wrong_environment")
	a.expectError(t, ctx, owner, a.environment, "missing-"+uuid.NewString(), "not_found")
}

func (a *nativeHarnessArtifact) observeActive(t *testing.T, ctx context.Context, local string) {
	t.Helper()
	owner := a.current(t)
	if !a.owners[owner.PID] {
		t.Fatal("active metadata did not use the prepared owner")
	}
	a.metadata(t, ctx, owner, "active_cancel", "retained.txt", local)
	a.metadata(t, ctx, owner, "active_cancel", "cancel.started", local)
}

func (a *nativeHarnessArtifact) assertReleased(t *testing.T, ctx context.Context) {
	t.Helper()
	for _, owner := range a.readyOwners {
		awaitDaemonRemoteCondition(t, ctx, 10*time.Second, "released metadata endpoint closure", func() bool {
			conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "unix", filepath.Join(owner.IPCRoot, "files.sock"))
			if err == nil {
				_ = conn.Close()
				return false
			}
			return errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED)
		})
	}
	a.proof["released_metadata_endpoints_closed"] = true
}

func (a *nativeHarnessArtifact) metadata(t *testing.T, ctx context.Context, owner nativeHarnessOwner, phase, path, local string) {
	t.Helper()
	response := a.request(t, ctx, owner, a.environment, path)
	var metadata struct {
		Size      int64 `json:"size"`
		IsFile    bool  `json:"is_file"`
		IsSymlink bool  `json:"is_symlink"`
	}
	info, err := os.Stat(filepath.Join(local, path))
	if err != nil || json.Unmarshal(response.Metadata, &metadata) != nil || response.Error != "" || !metadata.IsFile || metadata.IsSymlink || metadata.Size != info.Size() {
		t.Fatal("artifact metadata differs from independently observed remote file", phase, path)
	}
	observations := a.proof["metadata_observations"].([]map[string]any)
	a.proof["metadata_observations"] = append(observations, map[string]any{"phase": phase, "path": path, "owner": owner, "metadata": response.Metadata})
}

func (a *nativeHarnessArtifact) expectError(t *testing.T, ctx context.Context, owner nativeHarnessOwner, environment, path, expected string) {
	t.Helper()
	response := a.request(t, ctx, owner, environment, path)
	if response.Error != expected || len(response.Metadata) != 0 {
		t.Fatal("private artifact metadata error differs", expected, response.Error)
	}
	a.proof[expected+"_verified"] = true
}

type nativeHarnessMetadataResponse struct {
	Metadata json.RawMessage `json:"metadata"`
	Error    string          `json:"error"`
}

func (a *nativeHarnessArtifact) request(t *testing.T, ctx context.Context, owner nativeHarnessOwner, environment, path string) nativeHarnessMetadataResponse {
	t.Helper()
	requestCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(requestCtx, "unix", filepath.Join(owner.IPCRoot, "files.sock"))
	if err != nil {
		t.Fatal("private artifact metadata connection failed", err)
	}
	defer conn.Close()
	deadline, _ := requestCtx.Deadline()
	if err = conn.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	if err = json.NewEncoder(conn).Encode(map[string]string{"environment_id": environment, "path": path}); err != nil {
		t.Fatal(err)
	}
	var response nativeHarnessMetadataResponse
	if err = json.NewDecoder(io.LimitReader(conn, 16*1024)).Decode(&response); err != nil {
		t.Fatal("private artifact metadata response failed", err)
	}
	return response
}

func nativeHarnessFileHash(t *testing.T, path string) string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err = io.Copy(hash, file); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

const nativeHarnessWrapper = `#!/usr/bin/env python3
import hashlib
import json
import os
import pathlib
import sys
import uuid

native = %s
if "app-server" not in sys.argv[1:]:
    os.execv(native, [native, *sys.argv[1:]])
binding = json.loads(pathlib.Path(%s).read_text())
with open(binding["artifact"], "rb") as source:
    hasher = hashlib.sha256()
    for block in iter(lambda: source.read(1024 * 1024), b""):
        hasher.update(block)
    digest = hasher.hexdigest()
if digest != binding["artifact_sha256"]:
    raise SystemExit("private artifact identity changed")
ipc = str(pathlib.Path(binding["root"]) / ("p-" + uuid.uuid4().hex[:12]))
environment = dict(os.environ)
environment.update({
    "PARSAR_CODEX_HARNESS_NATIVE": binding["native"],
    "PARSAR_CODEX_HARNESS_ENVIRONMENT": binding["environment"],
    "PARSAR_CODEX_HARNESS_WORKSPACE": binding["workspace"],
    "PARSAR_CODEX_HARNESS_IPC_ROOT": ipc,
})
record = json.dumps({"pid": os.getpid(), "ipc_root": ipc, "artifact_sha256": digest}) + "\n"
fd = os.open(str(pathlib.Path(binding["root"]) / "owners.jsonl"), os.O_WRONLY | os.O_APPEND | os.O_CREAT, 0o600)
with os.fdopen(fd, "w") as output:
    output.write(record)
os.execve(binding["artifact"], [binding["artifact"], *sys.argv[1:]], environment)
`
