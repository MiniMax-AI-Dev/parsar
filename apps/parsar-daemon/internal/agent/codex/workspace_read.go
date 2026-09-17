package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"path/filepath"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
)

const workspaceReadMaxBytes = 8 << 20
const workspaceReadTimeout = 12 * time.Second

var _ agent.WorkspaceReader = (*Prepared)(nil)
var _ agent.WorkspaceReader = (*Session)(nil)

func (p *Prepared) ReadWorkspaceFile(ctx context.Context, path string, maxBytes int) (agent.WorkspaceReadResult, error) {
	p.mu.Lock()
	if p.claimed || p.closed || p.started {
		p.mu.Unlock()
		return agent.WorkspaceReadResult{}, agent.ErrWorkspaceReadUnavailable
	}
	frame, err := p.session.admitWorkspaceRead(ctx, path, maxBytes)
	p.mu.Unlock()
	if err != nil {
		return agent.WorkspaceReadResult{}, err
	}
	return p.session.harness.readWorkspaceFile(ctx, frame, maxBytes)
}

func (s *Session) ReadWorkspaceFile(ctx context.Context, path string, maxBytes int) (agent.WorkspaceReadResult, error) {
	frame, err := s.admitWorkspaceRead(ctx, path, maxBytes)
	if err != nil {
		return agent.WorkspaceReadResult{}, err
	}
	return s.harness.readWorkspaceFile(ctx, frame, maxBytes)
}

func (s *Session) admitWorkspaceRead(ctx context.Context, path string, maxBytes int) ([]byte, error) {
	if s.harness == nil {
		return nil, agent.ErrWorkspaceReadUnsupported
	}
	if ctx == nil || ctx.Err() != nil || s.cancelCtx.Err() != nil || s.cancelled.Load() || s.terminal.Load() || !s.rpc.Alive() {
		return nil, agent.ErrWorkspaceReadUnavailable
	}
	if maxBytes < 1 || maxBytes > workspaceReadMaxBytes || path == "" || len(path) > 8192 || strings.ContainsAny(path, "\x00\\\r\n") {
		return nil, agent.ErrWorkspaceReadInvalid
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." {
			return nil, agent.ErrWorkspaceReadInvalid
		}
	}
	frame, err := json.Marshal(struct {
		Environment string `json:"environment_id"`
		Operation   string `json:"operation"`
		Path        string `json:"path"`
		MaxBytes    int    `json:"max_bytes"`
	}{s.harness.environment, "read", path, maxBytes})
	if err != nil || len(frame)+1 > 8192 {
		return nil, agent.ErrWorkspaceReadInvalid
	}
	h := s.harness
	h.readMu.Lock()
	defer h.readMu.Unlock()
	if h.uncertain {
		return nil, agent.ErrWorkspaceReadUncertain
	}
	if h.reading {
		return nil, agent.ErrWorkspaceReadBusy
	}
	h.reading = true
	return append(frame, '\n'), nil
}

func (h *privateHarness) readWorkspaceFile(ctx context.Context, frame []byte, maxBytes int) (result agent.WorkspaceReadResult, err error) {
	defer func() {
		h.readMu.Lock()
		h.reading = false
		h.uncertain = h.uncertain || errors.Is(err, agent.ErrWorkspaceReadUncertain)
		h.readMu.Unlock()
	}()
	deadline := time.Now().Add(workspaceReadTimeout)
	if requested, ok := ctx.Deadline(); ok && requested.Before(deadline) {
		deadline = requested
	}
	// Cancellation cannot discard an admitted native wait; only its fixed deadline can.
	operation, cancel := context.WithDeadline(context.WithoutCancel(ctx), deadline)
	defer cancel()
	conn, dialErr := (&net.Dialer{}).DialContext(operation, "unix", filepath.Join(h.root, "files.sock"))
	if dialErr != nil {
		return result, agent.ErrWorkspaceReadUnavailable
	}
	defer conn.Close()
	if conn.SetDeadline(deadline) != nil {
		return result, agent.ErrWorkspaceReadUnavailable
	}
	if n, writeErr := conn.Write(frame); writeErr != nil || n != len(frame) {
		return result, agent.ErrWorkspaceReadUncertain
	}
	limit := base64.StdEncoding.EncodedLen(maxBytes) + 1024
	response, readErr := bufio.NewReader(io.LimitReader(conn, int64(limit+1))).ReadBytes('\n')
	if readErr != nil || len(response) > limit {
		return result, agent.ErrWorkspaceReadUncertain
	}
	return decodeWorkspaceRead(response, maxBytes)
}

func decodeWorkspaceRead(frame []byte, maxBytes int) (agent.WorkspaceReadResult, error) {
	var response struct {
		Error *string `json:"error"`
		Read  *struct {
			Data      *string `json:"data_base64"`
			Truncated *bool   `json:"truncated"`
			Closed    *bool   `json:"close_acknowledged"`
		} `json:"read"`
	}
	decoder := json.NewDecoder(bytes.NewReader(frame))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&response) != nil || decoder.Decode(new(any)) != io.EOF || (response.Error == nil) == (response.Read == nil) {
		return agent.WorkspaceReadResult{}, agent.ErrWorkspaceReadUncertain
	}
	if response.Error != nil {
		switch *response.Error {
		case "not_found":
			return agent.WorkspaceReadResult{}, fs.ErrNotExist
		case "permission_denied":
			return agent.WorkspaceReadResult{}, fs.ErrPermission
		case "invalid_request", "invalid_path":
			return agent.WorkspaceReadResult{}, agent.ErrWorkspaceReadInvalid
		case "environment_unavailable", "wrong_environment", "native_error":
			return agent.WorkspaceReadResult{}, agent.ErrWorkspaceReadUnavailable
		default:
			return agent.WorkspaceReadResult{}, agent.ErrWorkspaceReadUncertain
		}
	}
	read := response.Read
	if read.Data == nil || read.Truncated == nil || read.Closed == nil || !*read.Closed {
		return agent.WorkspaceReadResult{}, agent.ErrWorkspaceReadUncertain
	}
	data, err := base64.StdEncoding.Strict().DecodeString(*read.Data)
	if err != nil || base64.StdEncoding.EncodeToString(data) != *read.Data || len(data) > maxBytes || (*read.Truncated && len(data) != maxBytes) {
		return agent.WorkspaceReadResult{}, agent.ErrWorkspaceReadUncertain
	}
	return agent.WorkspaceReadResult{Data: data, Truncated: *read.Truncated}, nil
}
