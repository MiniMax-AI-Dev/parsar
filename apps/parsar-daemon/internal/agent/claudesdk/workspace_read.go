package claudesdk

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/google/uuid"
)

const workspaceReadMaxBytes = 1 << 20
const workspaceReadTimeout = 12 * time.Second

type workspaceReadState struct {
	mu        sync.Mutex
	supported bool
	closed    bool
	uncertain bool
	pending   *workspaceRead
}
type workspaceRead struct {
	id       string
	maxBytes int
	done     chan struct{}
	result   agent.WorkspaceReadResult
	err      error
}
type workspaceReadEvent struct {
	Type      string  `json:"type"`
	ID        string  `json:"id"`
	Data      *string `json:"data_base64"`
	Truncated *bool   `json:"truncated"`
	Error     string  `json:"error"`
}

var _ agent.WorkspaceReader = (*prepared)(nil)
var _ agent.WorkspaceReader = (*session)(nil)

func (p *prepared) ReadWorkspaceFile(ctx context.Context, path string, maxBytes int) (agent.WorkspaceReadResult, error) {
	p.mu.Lock()
	if p.closed || p.binding != nil {
		p.mu.Unlock()
		return agent.WorkspaceReadResult{}, agent.ErrWorkspaceReadUnavailable
	}
	read, err := p.session.admitWorkspaceRead(ctx, path, maxBytes)
	p.mu.Unlock()
	if err != nil {
		return agent.WorkspaceReadResult{}, err
	}
	return p.session.awaitWorkspaceRead(ctx, read)
}

func (s *session) ReadWorkspaceFile(ctx context.Context, path string, maxBytes int) (agent.WorkspaceReadResult, error) {
	read, err := s.admitWorkspaceRead(ctx, path, maxBytes)
	if err != nil {
		return agent.WorkspaceReadResult{}, err
	}
	return s.awaitWorkspaceRead(ctx, read)
}

func (s *session) admitWorkspaceRead(ctx context.Context, path string, maxBytes int) (*workspaceRead, error) {
	w := &s.reads
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
	if maxBytes < 1 || maxBytes > workspaceReadMaxBytes || path == "" || len(path) > 8192 || strings.ContainsAny(path, "\x00\\\r\n") {
		return nil, agent.ErrWorkspaceReadInvalid
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." {
			return nil, agent.ErrWorkspaceReadInvalid
		}
	}
	if w.pending != nil {
		return nil, agent.ErrWorkspaceReadBusy
	}
	read := &workspaceRead{id: uuid.NewString(), maxBytes: maxBytes, done: make(chan struct{})}
	frame, err := json.Marshal(struct {
		Type     string `json:"type"`
		ID       string `json:"id"`
		Path     string `json:"path"`
		MaxBytes int    `json:"max_bytes"`
	}{"workspace_read", read.id, path, maxBytes})
	if err != nil || len(frame)+1 > 8192 {
		return nil, agent.ErrWorkspaceReadInvalid
	}
	w.pending = read
	deadline := time.Now().Add(workspaceReadTimeout)
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
		s.failWorkspaceRead(read)
	}()
	go func() {
		s.writeMu.Lock()
		_, err := s.process.Stdin.Write(append(frame, '\n'))
		s.writeMu.Unlock()
		if err != nil {
			s.failWorkspaceRead(read)
		}
	}()
	return read, nil
}

func (s *session) failWorkspaceRead(read *workspaceRead) {
	s.reads.mu.Lock()
	pending := s.reads.pending == read
	if pending {
		s.reads.uncertain = true
		s.reads.pending = nil
		read.err = agent.ErrWorkspaceReadUncertain
		s.process.Cancel()
		close(read.done)
	}
	s.reads.mu.Unlock()
}

func (s *session) awaitWorkspaceRead(_ context.Context, read *workspaceRead) (agent.WorkspaceReadResult, error) {
	// Caller cancellation cannot discard an already admitted native wait.
	<-read.done
	return read.result, read.err
}

func (s *session) receiveWorkspaceRead(raw []byte) bool {
	var event workspaceReadEvent
	if json.Unmarshal(raw, &event) != nil || event.Type != "workspace_read" {
		return false
	}
	w := &s.reads
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
	if event.Error != "" && event.Data == nil && event.Truncated == nil {
		switch event.Error {
		case "invalid":
			read.err = agent.ErrWorkspaceReadInvalid
		case "busy":
			read.err = agent.ErrWorkspaceReadBusy
		case "unavailable":
			read.err = agent.ErrWorkspaceReadUnavailable
		}
	} else if event.Error == "" && event.Data != nil && event.Truncated != nil {
		data, err := base64.StdEncoding.Strict().DecodeString(*event.Data)
		if err == nil && base64.StdEncoding.EncodeToString(data) == *event.Data && len(data) <= read.maxBytes && (!*event.Truncated || len(data) == read.maxBytes) {
			read.result = agent.WorkspaceReadResult{Data: data, Truncated: *event.Truncated}
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

func (s *session) stopWorkspaceReads() {
	w := &s.reads
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
