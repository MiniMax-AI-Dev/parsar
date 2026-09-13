package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Request sends a JSON-RPC call and blocks until the response arrives,
// the deadline fires, or the child exits. Returns the raw result JSON
// so the caller can pick its decode shape.
func (c *JSONRPCClient) Request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	return c.request(ctx, method, params, c.writeFrame)
}

func (c *JSONRPCClient) request(ctx context.Context, method string, params any, write func(any) error) (json.RawMessage, error) {
	return c.requestWithTimeout(ctx, method, params, write, c.cfg.RequestTimeout)
}

func (c *JSONRPCClient) requestWithTimeout(ctx context.Context, method string, params any, write func(any) error, timeout time.Duration) (json.RawMessage, error) {
	if !c.Alive() {
		return nil, errors.New("codex rpc: client not alive")
	}
	id, err := newRequestID()
	if err != nil {
		return nil, fmt.Errorf("codex rpc: id: %w", err)
	}
	pending := &pendingRequest{
		method: method,
		resp:   make(chan rpcResponse, 1),
	}
	c.pendingMu.Lock()
	c.pending[id] = pending
	c.pendingMu.Unlock()

	frame := JsonRpcRequest{JsonRpc: JsonRpcVersion, ID: id, Method: method, Params: params}
	if err := write(frame); err != nil {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return nil, fmt.Errorf("codex rpc: write %s: %w", method, err)
	}

	var deadline <-chan time.Time
	if timeout > 0 {
		c.pendingMu.Lock()
		timer := time.NewTimer(timeout)
		if c.pending[id] == pending {
			pending.timer = timer
		} else {
			timer.Stop()
		}
		c.pendingMu.Unlock()
		defer timer.Stop()
		deadline = timer.C
	}

	select {
	case r := <-pending.resp:
		return r.result, r.err
	case <-deadline:
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return nil, fmt.Errorf("codex rpc: %s timed out after %s", method, timeout)
	case <-ctx.Done():
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return nil, ctx.Err()
	}
}
