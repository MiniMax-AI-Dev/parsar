package claudesdk

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"strings"
	"sync"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/google/uuid"
)

const workspaceDirectoryMaxEntries = 1000
const workspaceDirectoryTimeout = 12 * time.Second

type workspaceDirectoryState struct {
	mu        sync.Mutex
	supported bool
	closed    bool
	uncertain bool
	pending   *workspaceDirectory
}
type workspaceDirectory struct {
	id         string
	maxEntries int
	done       chan struct{}
	result     agent.WorkspaceDirectoryResult
	err        error
}
type workspaceDirectoryEvent struct {
	Type      string                     `json:"type"`
	ID        string                     `json:"id"`
	Entries   *[]workspaceDirectoryEntry `json:"entries"`
	Truncated *bool                      `json:"truncated"`
	Error     string                     `json:"error"`
}

var _ agent.WorkspaceDirectoryLister = (*prepared)(nil)
var _ agent.WorkspaceDirectoryLister = (*session)(nil)

func (p *prepared) ListWorkspaceDirectory(ctx context.Context, path string, maxEntries int) (agent.WorkspaceDirectoryResult, error) {
	p.mu.Lock()
	if p.closed || p.binding != nil {
		p.mu.Unlock()
		return agent.WorkspaceDirectoryResult{}, agent.ErrWorkspaceReadUnavailable
	}
	read, err := p.session.admitWorkspaceDirectory(ctx, path, maxEntries)
	p.mu.Unlock()
	if err != nil {
		return agent.WorkspaceDirectoryResult{}, err
	}
	return p.session.awaitWorkspaceDirectory(ctx, read)
}

func (s *session) ListWorkspaceDirectory(ctx context.Context, path string, maxEntries int) (agent.WorkspaceDirectoryResult, error) {
	read, err := s.admitWorkspaceDirectory(ctx, path, maxEntries)
	if err != nil {
		return agent.WorkspaceDirectoryResult{}, err
	}
	return s.awaitWorkspaceDirectory(ctx, read)
}

func (s *session) admitWorkspaceDirectory(ctx context.Context, path string, maxEntries int) (*workspaceDirectory, error) {
	w := &s.directories
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.supported {
		return nil, agent.ErrWorkspaceReadUnsupported
	}
	if w.uncertain {
		return nil, agent.ErrWorkspaceReadUncertain
	}
	if w.closed || ctx == nil || ctx.Err() != nil || s.process.Context().Err() != nil {
		return nil, agent.ErrWorkspaceReadUnavailable
	}
	if maxEntries < 1 || maxEntries > workspaceDirectoryMaxEntries || len(path) > 8192 || strings.ContainsAny(path, "\x00\\\r\n") {
		return nil, agent.ErrWorkspaceReadInvalid
	}
	for _, part := range strings.Split(path, "/") {
		if path == "" {
			break
		}
		if part == "" || part == "." || part == ".." {
			return nil, agent.ErrWorkspaceReadInvalid
		}
	}
	if w.pending != nil {
		return nil, agent.ErrWorkspaceReadBusy
	}
	read := &workspaceDirectory{id: uuid.NewString(), maxEntries: maxEntries, done: make(chan struct{})}
	frame, err := json.Marshal(struct {
		Type       string `json:"type"`
		ID         string `json:"id"`
		Path       string `json:"directory"`
		MaxEntries int    `json:"max_entries"`
	}{"workspace_directory", read.id, path, maxEntries})
	if err != nil || len(frame)+1 > 8192 {
		return nil, agent.ErrWorkspaceReadInvalid
	}
	w.pending = read
	deadline := time.Now().Add(workspaceDirectoryTimeout)
	if requested, ok := ctx.Deadline(); ok && requested.Before(deadline) {
		deadline = requested
	}
	go func() {
		timer := time.NewTimer(time.Until(deadline))
		defer timer.Stop()
		select {
		case <-read.done:
			return
		case <-timer.C:
		}
		s.failWorkspaceDirectory(read)
	}()
	go func() {
		s.writeMu.Lock()
		_, err := s.process.Stdin.Write(append(frame, '\n'))
		s.writeMu.Unlock()
		if err != nil {
			s.failWorkspaceDirectory(read)
		}
	}()
	return read, nil
}

func (s *session) failWorkspaceDirectory(read *workspaceDirectory) {
	s.directories.mu.Lock()
	pending := s.directories.pending == read
	if pending {
		s.directories.uncertain = true
		s.directories.pending = nil
		read.err = agent.ErrWorkspaceReadUncertain
		s.process.Cancel()
		close(read.done)
	}
	s.directories.mu.Unlock()
}

func (s *session) awaitWorkspaceDirectory(_ context.Context, read *workspaceDirectory) (agent.WorkspaceDirectoryResult, error) {
	// Caller cancellation cannot discard an already admitted native wait.
	<-read.done
	return read.result, read.err
}

func (s *session) receiveWorkspaceDirectory(raw []byte) bool {
	var event workspaceDirectoryEvent
	if json.Unmarshal(raw, &event) != nil || event.Type != "workspace_directory" {
		return false
	}
	w := &s.directories
	w.mu.Lock()
	defer w.mu.Unlock()
	read := w.pending
	if read == nil || event.ID != read.id {
		w.uncertain = true
		s.process.Cancel()
		return true
	}
	read.err = agent.ErrWorkspaceReadUncertain
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	valid := decoder.Decode(&event) == nil && decoder.Decode(new(any)) == io.EOF
	if !valid {
		w.uncertain = true
		w.pending = nil
		close(read.done)
		s.process.Cancel()
		return true
	}
	if event.Error != "" && event.Entries == nil && event.Truncated == nil {
		switch event.Error {
		case "not_found":
			read.err = fs.ErrNotExist
		case "permission":
			read.err = fs.ErrPermission
		case "invalid":
			read.err = agent.ErrWorkspaceReadInvalid
		case "busy":
			read.err = agent.ErrWorkspaceReadBusy
		case "unavailable":
			read.err = agent.ErrWorkspaceReadUnavailable
		}
	} else if event.Error == "" && event.Entries != nil && event.Truncated != nil {
		entries, valid := validWorkspaceDirectoryEntries(*event.Entries, read.maxEntries)
		if valid && (!*event.Truncated || len(entries) == read.maxEntries) {
			read.result = agent.WorkspaceDirectoryResult{Entries: entries, Truncated: *event.Truncated}
			read.err = nil
		}
	}
	if read.err == agent.ErrWorkspaceReadUncertain {
		w.uncertain = true
		s.process.Cancel()
	}
	w.pending = nil
	close(read.done)
	return true
}

func (s *session) stopWorkspaceDirectories() {
	w := &s.directories
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	if w.pending != nil {
		read := w.pending
		w.pending = nil
		w.uncertain = true
		read.err = agent.ErrWorkspaceReadUncertain
		close(read.done)
	}
}

type workspaceDirectoryEntry struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	SizeBytes *int64 `json:"size_bytes,omitempty"`
}

func validWorkspaceDirectoryEntries(raw []workspaceDirectoryEntry, maxEntries int) ([]agent.WorkspaceDirectoryEntry, bool) {
	if raw == nil || len(raw) > maxEntries {
		return nil, false
	}
	entries := make([]agent.WorkspaceDirectoryEntry, 0, len(raw))
	names := make(map[string]bool, len(raw))
	for _, entry := range raw {
		if entry.Name == "" || entry.Name == "." || entry.Name == ".." || strings.ContainsAny(entry.Name, "/\x00") || names[entry.Name] {
			return nil, false
		}
		names[entry.Name] = true
		switch entry.Kind {
		case "file":
			if entry.SizeBytes == nil || *entry.SizeBytes < 0 {
				return nil, false
			}
		case "directory", "symlink", "other":
			if entry.SizeBytes != nil {
				return nil, false
			}
		default:
			return nil, false
		}
		entries = append(entries, agent.WorkspaceDirectoryEntry{Name: entry.Name, Kind: entry.Kind, SizeBytes: entry.SizeBytes})
	}
	return entries, true
}
