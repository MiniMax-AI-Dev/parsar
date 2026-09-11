package authoring

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type Sender interface {
	Send(context.Context, proto.Envelope) error
}

type Bridge struct {
	sender  Sender
	mu      sync.Mutex
	waiters map[string]waiter
}

type waiter struct {
	runID    string
	response chan proto.AuthoringResponsePayload
}

func New(sender Sender) *Bridge {
	return &Bridge{sender: sender, waiters: make(map[string]waiter)}
}

// Deliver only resolves the matching request from the same active run.
func (b *Bridge) Deliver(env proto.Envelope) {
	var response proto.AuthoringResponsePayload
	if env.DecodePayload(&response) != nil {
		return
	}
	b.mu.Lock()
	w, ok := b.waiters[response.RequestID]
	b.mu.Unlock()
	if ok && w.runID == env.ID {
		select {
		case w.response <- response:
		default:
		}
	}
}

func (b *Bridge) request(ctx context.Context, runID string, request proto.AuthoringRequestPayload) (proto.AuthoringResponsePayload, error) {
	request.RequestID = uuid.NewString()
	w := waiter{runID: runID, response: make(chan proto.AuthoringResponsePayload, 1)}
	b.mu.Lock()
	b.waiters[request.RequestID] = w
	b.mu.Unlock()
	defer func() { b.mu.Lock(); delete(b.waiters, request.RequestID); b.mu.Unlock() }()
	env, err := proto.NewEnvelope(proto.TypeAuthoringRequest, runID, request)
	if err != nil {
		return proto.AuthoringResponsePayload{}, err
	}
	if err := b.sender.Send(ctx, env); err != nil {
		return proto.AuthoringResponsePayload{}, err
	}
	select {
	case response := <-w.response:
		return response, nil
	case <-ctx.Done():
		return proto.AuthoringResponsePayload{}, ctx.Err()
	}
}

func (b *Bridge) Listen(parent context.Context, runID string) (string, func(), error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", nil, err
	}
	dir := filepath.Join(home, ".parsar", "authoring")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", nil, err
	}
	path := filepath.Join(dir, uuid.NewString()[:8]+".sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		return "", nil, fmt.Errorf("open daemon authoring socket: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		return "", nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(ctx, func() { _ = listener.Close() })
	closeListener := func() { cancel(); stop(); _ = listener.Close() }
	go func() {
		defer closeListener()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go b.serve(ctx, runID, conn)
		}
	}()
	return path, closeListener, nil
}

func (b *Bridge) serve(parent context.Context, runID string, conn net.Conn) {
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	var request proto.AuthoringRequestPayload
	decoder := json.NewDecoder(io.LimitReader(conn, proto.AuthoringMaxBytes+1))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&request)
	if err == nil && decoder.InputOffset() > proto.AuthoringMaxBytes {
		err = errors.New("authoring request is too large")
	}
	var response proto.AuthoringResponsePayload
	if err == nil {
		response, err = b.request(ctx, runID, request)
	}
	if err != nil {
		response.Error = err.Error()
	}
	_ = json.NewEncoder(conn).Encode(response)
}
