package localworkspace

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func (b *Binding) ListWorkspaceDirectory(ctx context.Context, path string, limit int) (agent.WorkspaceDirectoryResult, error) {
	if limit < 1 || limit > proto.WorkspaceDirectoryMaxEntries || len(path) > 4096 || strings.ContainsAny(path, "\\\x00\r\n") || (path != "" && (path == "." || !fs.ValidPath(path))) {
		return agent.WorkspaceDirectoryResult{}, agent.ErrWorkspaceReadInvalid
	}
	operation, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(operation, b.helper, b.workspace, path, strconv.Itoa(limit))
	cmd.Dir = "/"
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8"}
	cmd.WaitDelay = time.Second
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return agent.WorkspaceDirectoryResult{}, agent.ErrWorkspaceReadUnavailable
	}
	if err := cmd.Start(); err != nil {
		return agent.WorkspaceDirectoryResult{}, agent.ErrWorkspaceReadUnavailable
	}
	const maxOutput = 4 << 20
	data, err := io.ReadAll(io.LimitReader(pipe, maxOutput+1))
	if err != nil || len(data) > maxOutput {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if err != nil || waitErr != nil || len(data) > maxOutput {
		return agent.WorkspaceDirectoryResult{}, agent.ErrWorkspaceReadUncertain
	}
	return decodeDirectory(data, limit)
}

func decodeDirectory(data []byte, limit int) (agent.WorkspaceDirectoryResult, error) {
	var wire struct {
		Version   int                             `json:"version"`
		Error     *string                         `json:"error"`
		Directory *proto.WorkspaceDirectoryResult `json:"directory"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&wire) != nil || decoder.Decode(new(any)) != io.EOF || wire.Version != 1 || (wire.Error == nil) == (wire.Directory == nil) {
		return agent.WorkspaceDirectoryResult{}, agent.ErrWorkspaceReadUncertain
	}
	if wire.Error != nil {
		err := agent.ErrWorkspaceReadUncertain
		switch *wire.Error {
		case "not_found":
			err = fs.ErrNotExist
		case "permission_denied":
			err = fs.ErrPermission
		case "invalid_path":
			err = agent.ErrWorkspaceReadInvalid
		}
		return agent.WorkspaceDirectoryResult{}, err
	}
	if !proto.ValidWorkspaceDirectory(wire.Directory, limit) {
		return agent.WorkspaceDirectoryResult{}, agent.ErrWorkspaceReadUncertain
	}
	result := agent.WorkspaceDirectoryResult{Entries: make([]agent.WorkspaceDirectoryEntry, 0, len(wire.Directory.Entries)), Truncated: wire.Directory.Truncated}
	for _, e := range wire.Directory.Entries {
		result.Entries = append(result.Entries, agent.WorkspaceDirectoryEntry{Name: e.Name, Kind: e.Kind, SizeBytes: e.SizeBytes})
	}
	return result, nil
}
