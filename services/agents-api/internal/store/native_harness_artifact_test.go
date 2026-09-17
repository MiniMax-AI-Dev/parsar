package store_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
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
	peer          *gateway.Session
	handle, runID string
	root          string
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
	configuration := map[string]string{"native": native, "artifact": artifact, "root": state}
	configuration["artifact_sha256"] = nativeHarnessFileHash(t, artifact)
	proof := map[string]any{
		"artifact": artifact, "artifact_sha256": configuration["artifact_sha256"],
		"native_helper": native, "native_helper_sha256": nativeHarnessFileHash(t, native),
		"execution_caller":        "existing daemon Codex adapter and Go JSONRPCClient; no launch wrapper",
		"preflight":               "stock helper discovery; opt-in artifact selected by the native adapter",
		"metadata_observations":   []map[string]any{},
		"read_observations":       []map[string]any{},
		"read_error_observations": []map[string]any{},
	}
	return &nativeHarnessArtifact{root: state, configuration: configuration, proof: proof, owners: make(map[int]bool)}
}

func (a *nativeHarnessArtifact) bind(t *testing.T, environment, workspace string) {
	t.Helper()
	a.environment = environment
	a.configuration["workspace"] = workspace
}

func (a *nativeHarnessArtifact) current(t *testing.T) nativeHarnessOwner {
	t.Helper()
	artifact, err := os.Stat(a.configuration["artifact"])
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatal(err)
	}
	var owners []nativeHarnessOwner
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 1 {
			continue
		}
		process := filepath.Join("/proc", entry.Name())
		executable, err := os.Stat(filepath.Join(process, "exe"))
		if err != nil || !os.SameFile(executable, artifact) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(process, "environ"))
		if err != nil {
			continue
		}
		var environment, workspace, root string
		for _, value := range strings.Split(string(data), "\x00") {
			switch {
			case strings.HasPrefix(value, "PARSAR_CODEX_HARNESS_ENVIRONMENT="):
				environment = strings.TrimPrefix(value, "PARSAR_CODEX_HARNESS_ENVIRONMENT=")
			case strings.HasPrefix(value, "PARSAR_CODEX_HARNESS_WORKSPACE="):
				workspace = strings.TrimPrefix(value, "PARSAR_CODEX_HARNESS_WORKSPACE=")
			case strings.HasPrefix(value, "PARSAR_CODEX_HARNESS_IPC_ROOT="):
				root = strings.TrimPrefix(value, "PARSAR_CODEX_HARNESS_IPC_ROOT=")
			}
		}
		if environment != a.environment {
			continue
		}
		if workspace != a.configuration["workspace"] || filepath.Dir(filepath.Dir(root)) != a.root || !strings.HasPrefix(filepath.Base(filepath.Dir(root)), "ch-") {
			t.Fatal("adapter private binding differs from the requested Environment")
		}
		digest := nativeHarnessFileHash(t, filepath.Join(process, "exe"))
		if digest != a.configuration["artifact_sha256"] {
			t.Fatal("executing artifact identity differs")
		}
		owners = append(owners, nativeHarnessOwner{PID: pid, IPCRoot: root, ArtifactSHA256: digest})
	}
	if len(owners) != 1 {
		t.Fatal("expected one actual adapter-owned artifact process", len(owners))
	}
	return owners[0]
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
	if phase == "first" {
		seedNativeHarnessReadFiles(t, local)
	}
	a.observeReads(t, ctx, owner, "ready_"+phase, local, phase != "first")
}

func (a *nativeHarnessArtifact) observeActive(t *testing.T, ctx context.Context, local string) {
	t.Helper()
	owner := a.current(t)
	if !a.owners[owner.PID] {
		t.Fatal("active metadata did not use the prepared owner")
	}
	a.metadata(t, ctx, owner, "active_cancel", "retained.txt", local)
	a.metadata(t, ctx, owner, "active_cancel", "cancel.started", local)
	a.observeReads(t, ctx, owner, "active_cancel", local, true)
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
	response := a.request(t, ctx, owner, map[string]any{"environment_id": a.environment, "path": path}, 16*1024)
	var metadata struct {
		Size      int64 `json:"size"`
		IsFile    bool  `json:"is_file"`
		IsSymlink bool  `json:"is_symlink"`
	}
	info, err := os.Stat(filepath.Join(local, path))
	if err != nil || json.Unmarshal(response.Metadata, &metadata) != nil || response.Error != "" || len(response.Read) != 0 || !metadata.IsFile || metadata.IsSymlink || metadata.Size != info.Size() {
		t.Fatal("artifact metadata differs from independently observed remote file", phase, path)
	}
	observations := a.proof["metadata_observations"].([]map[string]any)
	a.proof["metadata_observations"] = append(observations, map[string]any{"phase": phase, "path": path, "owner": owner, "metadata": response.Metadata})
}

func (a *nativeHarnessArtifact) expectError(t *testing.T, ctx context.Context, owner nativeHarnessOwner, environment, path, expected string) {
	t.Helper()
	response := a.request(t, ctx, owner, map[string]any{"environment_id": environment, "path": path}, 16*1024)
	if response.Error != expected || len(response.Metadata) != 0 || len(response.Read) != 0 {
		t.Fatal("private artifact metadata error differs", expected, response.Error)
	}
	a.proof[expected+"_verified"] = true
}

type nativeHarnessMetadataResponse struct {
	Metadata json.RawMessage `json:"metadata"`
	Read     json.RawMessage `json:"read"`
	Error    string          `json:"error"`
}

func (a *nativeHarnessArtifact) request(t *testing.T, ctx context.Context, owner nativeHarnessOwner, request map[string]any, responseLimit int64) nativeHarnessMetadataResponse {
	t.Helper()
	requestCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(requestCtx, "unix", filepath.Join(owner.IPCRoot, "files.sock"))
	if err != nil {
		t.Fatal("private artifact file connection failed", err)
	}
	defer conn.Close()
	deadline, _ := requestCtx.Deadline()
	if err = conn.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	if err = json.NewEncoder(conn).Encode(request); err != nil {
		t.Fatal(err)
	}
	var response nativeHarnessMetadataResponse
	if err = json.NewDecoder(io.LimitReader(conn, responseLimit)).Decode(&response); err != nil {
		t.Fatal("private artifact file response failed", err)
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
