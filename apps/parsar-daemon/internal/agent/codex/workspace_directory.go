package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
)

const workspaceDirectoryMaxEntries = 4096

var _ agent.WorkspaceDirectoryLister = (*Prepared)(nil)
var _ agent.WorkspaceDirectoryLister = (*Session)(nil)

func (p *Prepared) ListWorkspaceDirectory(ctx context.Context, path string, maxEntries int) (agent.WorkspaceDirectoryResult, error) {
	p.mu.Lock()
	if p.claimed || p.closed || p.started {
		p.mu.Unlock()
		return agent.WorkspaceDirectoryResult{}, agent.ErrWorkspaceReadUnavailable
	}
	frame, err := p.session.admitWorkspaceDirectory(ctx, path, maxEntries)
	p.mu.Unlock()
	if err != nil {
		return agent.WorkspaceDirectoryResult{}, err
	}
	return p.session.harness.readWorkspaceDirectory(ctx, frame, maxEntries)
}

func (s *Session) ListWorkspaceDirectory(ctx context.Context, path string, maxEntries int) (agent.WorkspaceDirectoryResult, error) {
	frame, err := s.admitWorkspaceDirectory(ctx, path, maxEntries)
	if err != nil {
		return agent.WorkspaceDirectoryResult{}, err
	}
	return s.harness.readWorkspaceDirectory(ctx, frame, maxEntries)
}

func (s *Session) admitWorkspaceDirectory(ctx context.Context, path string, maxEntries int) ([]byte, error) {
	if err := s.workspaceReadAvailable(ctx); err != nil {
		return nil, err
	}
	if !workspaceRelativePath(path, true) || maxEntries < 1 || maxEntries > workspaceDirectoryMaxEntries {
		return nil, agent.ErrWorkspaceReadInvalid
	}
	frame, err := json.Marshal(struct {
		Environment string `json:"environment_id"`
		Operation   string `json:"operation"`
		Path        string `json:"path"`
		MaxEntries  int    `json:"max_entries"`
	}{s.harness.environment, "list_directory", path, maxEntries})
	if err != nil {
		return nil, agent.ErrWorkspaceReadInvalid
	}
	return s.claimWorkspaceRead(frame)
}

func (h *privateHarness) readWorkspaceDirectory(ctx context.Context, frame []byte, maxEntries int) (result agent.WorkspaceDirectoryResult, err error) {
	err = h.exchangeWorkspaceRead(ctx, frame, maxEntries*2048+1024, func(response []byte) error {
		var decodeErr error
		result, decodeErr = decodeWorkspaceDirectory(response, maxEntries)
		return decodeErr
	})
	return result, err
}

func decodeWorkspaceDirectory(frame []byte, maxEntries int) (agent.WorkspaceDirectoryResult, error) {
	var response struct {
		Error     *string `json:"error"`
		Directory *struct {
			Entries *[]struct {
				Name      string `json:"name"`
				Kind      string `json:"kind"`
				SizeBytes *int64 `json:"size_bytes"`
			} `json:"entries"`
			Truncated *bool `json:"truncated"`
		} `json:"directory"`
	}
	invalid := agent.ErrWorkspaceReadUncertain
	decoder := json.NewDecoder(bytes.NewReader(frame))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&response) != nil || decoder.Decode(new(any)) != io.EOF || (response.Error == nil) == (response.Directory == nil) {
		return agent.WorkspaceDirectoryResult{}, invalid
	}
	if response.Error != nil {
		return agent.WorkspaceDirectoryResult{}, workspaceReadError(*response.Error)
	}
	directory := response.Directory
	if directory.Entries == nil || directory.Truncated == nil || len(*directory.Entries) > maxEntries {
		return agent.WorkspaceDirectoryResult{}, invalid
	}
	result := agent.WorkspaceDirectoryResult{Entries: make([]agent.WorkspaceDirectoryEntry, 0, len(*directory.Entries)), Truncated: *directory.Truncated}
	seen := make(map[string]bool, len(*directory.Entries))
	for _, entry := range *directory.Entries {
		if !workspaceRelativePath(entry.Name, false) || strings.Contains(entry.Name, "/") || seen[entry.Name] {
			return agent.WorkspaceDirectoryResult{}, invalid
		}
		seen[entry.Name] = true
		switch entry.Kind {
		case "file":
			if entry.SizeBytes == nil || *entry.SizeBytes < 0 {
				return agent.WorkspaceDirectoryResult{}, invalid
			}
		case "directory", "symlink", "other":
			if entry.SizeBytes != nil {
				return agent.WorkspaceDirectoryResult{}, invalid
			}
		default:
			return agent.WorkspaceDirectoryResult{}, invalid
		}
		result.Entries = append(result.Entries, agent.WorkspaceDirectoryEntry{Name: entry.Name, Kind: entry.Kind, SizeBytes: entry.SizeBytes})
	}
	return result, nil
}
