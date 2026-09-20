package execution

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

// Progress is process-local: a recovered running installation is never replayed.
type runtimeInitialization struct {
	owner       store.RuntimeAllocation
	next, count int
	files       int
	operations  []runtimeSetupOperation
	deadline    time.Time
}

func (r *runtimeLifecycle) observeInitialization(ctx context.Context, owner store.RuntimeAllocation) error {
	if owner.Initialization == "complete" {
		return nil
	}
	if owner.Initialization == "running" {
		if r.initializing != nil && r.initializing.owner.ID == owner.ID {
			return nil
		}
		_, err := r.store.RequestRuntimeCleanup(ctx, owner)
		return err
	}
	if r.initializing != nil {
		return nil
	}
	environment, err := r.store.GetEnvironment(ctx, owner.TenantID, owner.EnvironmentID)
	if err != nil {
		return err
	}
	var cfg struct {
		Initialization bool                        `json:"initialization"`
		Files          []store.InitialFileMetadata `json:"files"`
	}
	if json.Unmarshal(environment.Configuration, &cfg) != nil || len(cfg.Files) > 50 {
		_, err = r.store.RequestRuntimeCleanup(ctx, owner)
		return err
	}
	setup, err := r.store.ReadEnvironmentSetup(ctx, owner.TenantID, owner.SessionID)
	if err != nil {
		return err
	}
	operations := setupOperations(setup)
	if len(cfg.Files)+len(operations) == 0 || cfg.Initialization != !setup.Empty() {
		_, err = r.store.RequestRuntimeCleanup(ctx, owner)
		return err
	}
	claimed, err := r.store.ClaimRuntimeInitialization(ctx, owner)
	if err != nil {
		return err
	}
	r.initializing = &runtimeInitialization{owner: claimed, count: len(cfg.Files) + len(operations), files: len(cfg.Files), operations: operations, deadline: time.Now().Add(30 * time.Minute)}
	return nil
}

// One bounded operation follows a full maintenance scan; other allocations get serviced between operations.
func (r *runtimeLifecycle) advanceInitialization(ctx context.Context) error {
	active := r.initializing
	if active == nil {
		return nil
	}
	owner, err := r.store.GetRuntimeAllocation(ctx, active.owner.TenantID, active.owner.EnvironmentID)
	if err != nil {
		return err
	}
	if owner.State == "cleanup_pending" || owner.State == "released" || owner.SessionDeleted || owner.Expired {
		r.initializing = nil
		return nil
	}
	if owner.Initialization == "complete" {
		r.initializing = nil
		return nil
	}
	if owner.ID != active.owner.ID || owner.Initialization != "running" {
		r.initializing = nil
		return sandbox.ErrOwnership
	}
	operation, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if time.Now().After(active.deadline) {
		r.initializing = nil
		return sandbox.ErrCommandUnconfirmed
	}
	if err := r.store.CheckExecutionOwnership(operation); err != nil {
		r.initializing = nil
		return err
	}
	if active.next < active.files {
		var file store.InitialFileMetadata
		var body []byte
		file, body, err = r.store.ReadInitialEnvironmentFile(operation, owner.TenantID, owner.SessionID, active.next)
		if err == nil {
			err = installInitialFile(operation, r.config.Providers[owner.ProviderKey], runtimeReference(owner), file, body)
		}
	} else {
		err = runRuntimeSetup(operation, r.config.Providers[owner.ProviderKey], runtimeReference(owner), active.operations[active.next-active.files])
	}
	if err != nil {
		// Clearing the in-memory owner makes the next observation request cleanup,
		// even when the failed operation consumed its entire deadline.
		r.initializing = nil
		return err
	}
	active.next++
	if active.next == active.count {
		_, err = r.store.CompleteRuntimeInitialization(operation, owner)
		r.initializing = nil
		return err
	}
	return nil
}

func installInitialFile(ctx context.Context, provider sandbox.Provider, reference sandbox.Reference, file store.InitialFileMetadata, body []byte) error {
	if provider == nil || file.SizeBytes == nil || *file.SizeBytes != int64(len(body)) || len(body) > store.MaxInitialFileBytes || !strings.HasPrefix(file.Path, "/workspace/") {
		return sandbox.ErrInvalid
	}
	digest := sha256.Sum256(body)
	input := make([]byte, 0, len(body)+len(digest))
	input = append(input, body...)
	input = append(input, digest[:]...)
	result, err := provider.RunCommand(ctx, reference, sandbox.Command{Directory: "/", Args: []string{"/usr/bin/python3", "-I", "-S", "-c", initialFileInstaller, strings.TrimPrefix(file.Path, "/workspace/"), strconv.Itoa(len(body))}, Stdin: input})
	if err != nil {
		return err
	}
	var receipt struct {
		Version   int    `json:"version"`
		Outcome   string `json:"outcome"`
		SizeBytes *int64 `json:"size_bytes"`
	}
	if result.ExitCode != 0 || result.Stderr != "" || json.Unmarshal([]byte(result.Stdout), &receipt) != nil || receipt.Version != 1 || receipt.Outcome != "completed" || receipt.SizeBytes == nil || *receipt.SizeBytes != int64(len(body)) {
		return errors.New("initial environment file installation unconfirmed")
	}
	return nil
}

// Isolated Python creates only fd-anchored workspace parents, then replaces itself with the existing atomic writer.
const initialFileInstaller = `import os, sys
parts = sys.argv[1].split('/')
if any(not p or p in ('.', '..') or any(c in p for c in ('\\', '\x00', '\r', '\n')) for p in parts):
    raise SystemExit(2)
flags = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC
fd = os.open('/', flags)
for component in ('environment', 'workspace'):
    child = os.open(component, flags, dir_fd=fd)
    os.close(fd)
    fd = child
for component in parts[:-1]:
    try:
        os.mkdir(component, mode=0o700, dir_fd=fd)
    except FileExistsError:
        pass
    child = os.open(component, flags, dir_fd=fd)
    os.close(fd)
    fd = child
os.close(fd)
os.execv('/usr/local/bin/agents-api-codex-write', ['agents-api-codex-write', '/environment/workspace', sys.argv[1], sys.argv[2], '/environment/staging'])
`
