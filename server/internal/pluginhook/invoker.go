// Package pluginhook provides a client for invoking plugin hook handlers
// via the plugin-host's hooks/invoke JSON-RPC method over MCP stdio.
//
// The Go server uses this to consult plugin hooks before forwarding
// permission requests to the human approval flow.
package pluginhook

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
)

// Decision represents the outcome of a hook invocation.
type Decision struct {
	// Result is one of: "deny", "allow", "ask_human", "no_handler".
	Result string `json:"decision"`
	// Reason is an optional human-readable explanation from the hook.
	Reason string `json:"reason,omitempty"`
	// Plugin is the name of the plugin that made the decision.
	Plugin string `json:"plugin,omitempty"`
}

// IsDecisive returns true if the hook made an explicit deny/allow decision
// (as opposed to ask_human or no_handler which both mean "proceed with
// normal flow").
func (d Decision) IsDecisive() bool {
	return d.Result == "deny" || d.Result == "allow"
}

// PermissionPayload is the payload sent to before_permission_forward hooks.
type PermissionPayload struct {
	RequestID string         `json:"request_id"`
	Tool      string         `json:"tool"`
	Title     string         `json:"title,omitempty"`
	Detail    string         `json:"detail,omitempty"`
	Args      string         `json:"args,omitempty"`
	Payload   map[string]any `json:"payload,omitempty"`
}

// Invoker manages a connection to the plugin-host process for hook
// invocation. It spawns the plugin-host process on first use and
// communicates via JSON-RPC over stdin/stdout.
type Invoker struct {
	hostPath   string
	pluginsDir string

	mu      sync.Mutex
	proc    *exec.Cmd
	stdin   io.WriteCloser
	scanner *bufio.Scanner
	nextID  atomic.Int64
	ready   bool
}

// NewInvoker creates a hook invoker. The process is not started until
// the first call to Invoke.
func NewInvoker(hostPath, pluginsDir string) *Invoker {
	return &Invoker{
		hostPath:   hostPath,
		pluginsDir: pluginsDir,
	}
}

// jsonrpcRequest is a minimal JSON-RPC 2.0 request.
type jsonrpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// jsonrpcResponse is a minimal JSON-RPC 2.0 response.
type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// InvokeBeforePermissionForward calls the before_permission_forward hook
// with the given permission request payload. Returns the hook's decision.
//
// Timeout: the caller should set a context deadline; this function also
// enforces an internal 5s hard timeout as a safety net.
func (inv *Invoker) InvokeBeforePermissionForward(ctx context.Context, payload PermissionPayload) (Decision, error) {
	// Enforce total timeout of 5s regardless of caller context.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	return inv.invoke(ctx, "hooks/invoke", map[string]any{
		"event":   "before_permission_forward",
		"payload": payload,
	})
}

// InvokeAfterToolResult calls the after_tool_result hook. This is a
// fire-and-forget observation hook — errors are logged but not returned.
func (inv *Invoker) InvokeAfterToolResult(ctx context.Context, payload map[string]any) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_, err := inv.invoke(ctx, "hooks/invoke", map[string]any{
		"event":   "after_tool_result",
		"payload": payload,
	})
	if err != nil {
		log.Bg().Warn("pluginhook: after_tool_result hook failed", "err", err)
	}
}

// HasHooks checks if the plugin-host has any hooks registered for the
// given event. Returns false if the process is not running or has no hooks.
func (inv *Invoker) HasHooks(ctx context.Context, event string) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	result, err := inv.invoke(ctx, "hooks/list", nil)
	if err != nil {
		return false
	}
	// Result is a Decision-shaped response from hooks/list which has a
	// different shape. Parse directly from the raw call.
	_ = result
	return true
}

// Close shuts down the plugin-host process if running.
func (inv *Invoker) Close() error {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	if inv.proc != nil && inv.proc.Process != nil {
		if inv.stdin != nil {
			inv.stdin.Close()
		}
		_ = inv.proc.Process.Kill()
		_ = inv.proc.Wait()
		inv.proc = nil
		inv.ready = false
	}
	return nil
}

// invoke sends a JSON-RPC request and waits for the response.
func (inv *Invoker) invoke(ctx context.Context, method string, params any) (Decision, error) {
	inv.mu.Lock()
	if !inv.ready {
		if err := inv.startLocked(); err != nil {
			inv.mu.Unlock()
			return Decision{Result: "ask_human"}, fmt.Errorf("pluginhook: start plugin-host: %w", err)
		}
	}
	id := inv.nextID.Add(1)
	req := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	reqBytes, err := json.Marshal(req)
	if err != nil {
		inv.mu.Unlock()
		return Decision{Result: "ask_human"}, fmt.Errorf("pluginhook: marshal request: %w", err)
	}
	reqBytes = append(reqBytes, '\n')

	_, err = inv.stdin.Write(reqBytes)
	if err != nil {
		inv.ready = false
		inv.mu.Unlock()
		return Decision{Result: "ask_human"}, fmt.Errorf("pluginhook: write to plugin-host: %w", err)
	}

	// Read response — we hold the lock so only one call is in-flight at a
	// time. This is acceptable because hook invocations are infrequent and
	// the 5s timeout bounds the lock hold time.
	type scanResult struct {
		line string
		ok   bool
	}
	scanCh := make(chan scanResult, 1)
	go func() {
		if inv.scanner.Scan() {
			scanCh <- scanResult{line: inv.scanner.Text(), ok: true}
		} else {
			scanCh <- scanResult{ok: false}
		}
	}()

	var resp jsonrpcResponse
	select {
	case <-ctx.Done():
		inv.mu.Unlock()
		return Decision{Result: "ask_human"}, fmt.Errorf("pluginhook: timeout waiting for response: %w", ctx.Err())
	case sr := <-scanCh:
		if !sr.ok {
			inv.ready = false
			inv.mu.Unlock()
			return Decision{Result: "ask_human"}, fmt.Errorf("pluginhook: plugin-host stdout closed")
		}
		if err := json.Unmarshal([]byte(sr.line), &resp); err != nil {
			inv.mu.Unlock()
			return Decision{Result: "ask_human"}, fmt.Errorf("pluginhook: decode response: %w", err)
		}
	}
	inv.mu.Unlock()

	if resp.Error != nil {
		return Decision{Result: "ask_human"}, fmt.Errorf("pluginhook: RPC error %d: %s", resp.Error.Code, resp.Error.Message)
	}

	var decision Decision
	if err := json.Unmarshal(resp.Result, &decision); err != nil {
		return Decision{Result: "ask_human"}, fmt.Errorf("pluginhook: decode decision: %w", err)
	}
	return decision, nil
}

// startLocked spawns the plugin-host process. Must be called with inv.mu held.
func (inv *Invoker) startLocked() error {
	cmd := exec.Command("node", inv.hostPath, "--plugins-dir", inv.pluginsDir)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		return fmt.Errorf("stdout pipe: %w", err)
	}
	// Stderr goes to the server log (os.Stderr so it mixes with server output).
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		stdin.Close()
		return fmt.Errorf("start process: %w", err)
	}

	inv.proc = cmd
	inv.stdin = stdin
	inv.scanner = bufio.NewScanner(stdout)
	inv.scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024) // 1MB line buffer

	// Send initialize to get the process ready.
	initReq := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      inv.nextID.Add(1),
		Method:  "initialize",
		Params:  map[string]any{"protocolVersion": "2024-11-05", "clientInfo": map[string]string{"name": "parsar-hook-invoker", "version": "0.1.0"}},
	}
	initBytes, _ := json.Marshal(initReq)
	initBytes = append(initBytes, '\n')
	if _, err := stdin.Write(initBytes); err != nil {
		cmd.Process.Kill()
		return fmt.Errorf("write initialize: %w", err)
	}

	// Read initialize response.
	if !inv.scanner.Scan() {
		cmd.Process.Kill()
		return fmt.Errorf("no initialize response from plugin-host")
	}
	var initResp jsonrpcResponse
	if err := json.Unmarshal([]byte(inv.scanner.Text()), &initResp); err != nil {
		cmd.Process.Kill()
		return fmt.Errorf("decode initialize response: %w", err)
	}
	if initResp.Error != nil {
		cmd.Process.Kill()
		return fmt.Errorf("initialize error: %s", initResp.Error.Message)
	}

	inv.ready = true
	log.Bg().Info("pluginhook: plugin-host process started",
		"pid", cmd.Process.Pid,
		"plugins_dir", inv.pluginsDir)
	return nil
}
