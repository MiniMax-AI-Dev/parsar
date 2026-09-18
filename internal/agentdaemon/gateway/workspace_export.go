package gateway

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

// ExportWorkspaceOutputs consumes bounded chunks and requires the exporter completion receipt.
func (s *Session) ExportWorkspaceOutputs(ctx context.Context, request proto.WorkspaceExportPayload, consume func(io.Reader) error) error {
	request.Step = "begin"
	if !proto.ValidWorkspaceExportRequest(request) || consume == nil {
		return errors.New("agentdaemon gateway: invalid workspace export")
	}
	id := uuid.NewString()
	s.workspaceExportMu.Lock()
	if s.IsClosed() {
		s.workspaceExportMu.Unlock()
		return ErrSessionClosed
	}
	if len(s.workspaceExports) != 0 {
		s.workspaceExportMu.Unlock()
		return errors.New("agentdaemon gateway: workspace export capacity")
	}
	replies := make(chan proto.Envelope, 1)
	s.workspaceExports = map[string]chan proto.Envelope{id: replies}
	s.workspaceExportMu.Unlock()
	defer func() { s.workspaceExportMu.Lock(); delete(s.workspaceExports, id); s.workspaceExportMu.Unlock() }()
	ctx, cancel := context.WithTimeout(ctx, 180*time.Second)
	defer cancel()
	r := &workspaceExportReader{ctx: ctx, peer: s, id: id, replies: replies, request: request}
	defer func() {
		if !r.completed {
			stop, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			env, _ := proto.NewEnvelope(proto.TypeWorkspaceExport, id, proto.WorkspaceExportPayload{Step: "cancel"})
			_ = s.Send(stop, env)
		}
	}()
	if err := consume(r); err != nil {
		return err
	}
	// Archive decoders may stop at their trailer before the native exit receipt.
	_, err := io.Copy(io.Discard, r)
	return err
}

type workspaceExportReader struct {
	ctx       context.Context
	peer      *Session
	id        string
	replies   <-chan proto.Envelope
	request   proto.WorkspaceExportPayload
	data      []byte
	offset    int64
	completed bool
}

func (r *workspaceExportReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if len(r.data) > 0 {
		n := copy(p, r.data)
		r.data = r.data[n:]
		return n, nil
	}
	if r.completed {
		return 0, io.EOF
	}
	env, err := proto.NewEnvelope(proto.TypeWorkspaceExport, r.id, r.request)
	if err != nil {
		return 0, err
	}
	if err = r.peer.Send(r.ctx, env); err != nil {
		return 0, err
	}
	select {
	case env, ok := <-r.replies:
		if !ok {
			return 0, ErrSessionClosed
		}
		var result proto.WorkspaceExportResultPayload
		if len(env.Payload) > proto.WorkspaceExportMaxFrameBytes || env.DecodePayload(&result) != nil || result.Offset != r.offset {
			return 0, errors.New("agentdaemon gateway: invalid export receipt")
		}
		if result.Outcome == "completed" && len(result.Data) == 0 && result.ErrorCode == "" {
			r.completed = true
			return 0, io.EOF
		}
		if result.Outcome != "chunk" || result.ErrorCode != "" || len(result.Data) == 0 || len(result.Data) > proto.WorkspaceExportChunkBytes || int64(len(result.Data)) > proto.WorkspaceExportMaxBytes-r.offset {
			return 0, errors.New("agentdaemon gateway: workspace export incomplete")
		}
		r.data = result.Data
		r.offset += int64(len(result.Data))
		r.request = proto.WorkspaceExportPayload{Step: "next", Offset: r.offset}
		return r.Read(p)
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	case <-r.peer.closed:
		return 0, ErrSessionClosed
	}
}

func (s *Session) dispatchWorkspaceExport(env proto.Envelope) {
	s.workspaceExportMu.Lock()
	defer s.workspaceExportMu.Unlock()
	if replies := s.workspaceExports[env.ID]; replies != nil {
		select {
		case replies <- env:
		default:
			close(replies)
			delete(s.workspaceExports, env.ID)
		}
	}
}

func (s *Session) closeWorkspaceExports() {
	s.workspaceExportMu.Lock()
	defer s.workspaceExportMu.Unlock()
	for id, replies := range s.workspaceExports {
		close(replies)
		delete(s.workspaceExports, id)
	}
}
