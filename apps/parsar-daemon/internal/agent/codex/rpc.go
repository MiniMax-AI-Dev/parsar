package codex

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"sync"
	"time"

	obslog "github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
)

// rpcDefaultRequestTimeout caps a single JSON-RPC request waiting for a
// response. 60s is the same default mini-captain uses; it must be long
// enough for `thread/start` (which can spin up a fresh model context),
// short enough that a dead app-server doesn't pin the prompt forever.
const rpcDefaultRequestTimeout = 60 * time.Second

// rpcInitTimeout caps the initial JSON-RPC `initialize` handshake.
// Shorter than per-request so misconfigured environments fail fast.
const rpcInitTimeout = 10 * time.Second

// rpcKillTimeout is the grace period between SIGTERM and SIGKILL when
// the app-server child is being torn down.
const rpcKillTimeout = 3 * time.Second

// rpcStdoutBufferMax caps a single NDJSON line on stdout. Codex's
// aggregated_output frames can run large; 16 MiB matches the opencode
// adapter and is well above any realistic single-line payload.
const rpcStdoutBufferMax = 16 * 1024 * 1024

// JSONRPCConfig configures a JSONRPCClient. Zero-value fields fall
// through to sensible defaults.
type JSONRPCConfig struct {
	// Binary is the codex executable to spawn. Defaults to "codex" (PATH lookup).
	Binary string
	// Args added before "app-server --stdio". Useful for `-c key=value`
	// overrides without forcing CODEX_HOME indirection.
	ExtraArgs []string
	// EnableFeatures translates to repeated `--enable <feat>` flags.
	EnableFeatures []string
	// DisableFeatures translates to repeated `--disable <feat>` flags.
	DisableFeatures []string
	// Cwd is the working directory for the child process. Empty
	// inherits the daemon's cwd.
	Cwd string
	// Env is layered ON TOP of os.Environ() — set CODEX_HOME / OPENAI_API_KEY
	// here. Empty values are not filtered (codex distinguishes empty
	// from unset for some keys).
	Env []string
	// LogTag is the prefix carried on every internal log line.
	LogTag string
	// RequestTimeout overrides rpcDefaultRequestTimeout.
	RequestTimeout time.Duration
	// Logger is the structured logger to use. nil falls back to obslog.Bg().
	Logger *slog.Logger
}

// JSONRPCClient drives one `codex app-server --stdio` child process,
// speaking JSON-RPC 2.0 over stdin/stdout NDJSON. Three inbound message
// shapes are supported:
//
//   - response       (id + result|error)        → matched to a pending request
//   - notification   (method + params, no id)   → routed to a per-method handler
//   - server request (id + method + params)     → routed to a per-method handler,
//                                                 reply is sent automatically
//                                                 unless the handler returns
//                                                 DeferReply.
//
// stderr is line-pumped to the logger and never merged with stdout.
type JSONRPCClient struct {
	cfg JSONRPCConfig

	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser

	mu       sync.Mutex
	alive    bool
	exitCode *int

	pendingMu sync.Mutex
	pending   map[string]*pendingRequest

	handlersMu        sync.RWMutex
	notifHandlers     map[string][]NotificationHandler
	serverReqHandlers map[string]ServerRequestHandler
	anyNotifHandler   NotificationHandler

	closeOnce sync.Once
	doneCh    chan struct{}
}

// NotificationHandler is invoked for every inbound notification of a
// registered method. Returning an error logs but does not kill the
// session.
type NotificationHandler func(params json.RawMessage)

// ServerRequestHandler handles a server-initiated request. Return
// (DeferReply, nil) to take ownership of the reply (e.g. when a human
// approval card sits between request and response). Otherwise, the
// returned value is marshalled into the JSON-RPC response. Returning an
// error sends a JSON-RPC error response with code -32603.
type ServerRequestHandler func(params json.RawMessage, id any) (any, error)

// DeferReply is the sentinel a ServerRequestHandler returns to take
// ownership of the eventual reply (call SendServerReply / SendServerError
// later). errors.Is(err, DeferReply) is false; the sentinel is matched
// by value identity, NOT errors.Is, so it's not an `error`.
type deferReplySentinel struct{}

// DeferReply, when returned from a ServerRequestHandler, signals that
// the handler will reply later via SendServerReply.
var DeferReply = deferReplySentinel{}

type pendingRequest struct {
	method string
	resp   chan rpcResponse
	timer  *time.Timer
}

type rpcResponse struct {
	result json.RawMessage
	err    error
}

// NewJSONRPCClient builds a client. The child is not spawned until
// Start is called.
func NewJSONRPCClient(cfg JSONRPCConfig) *JSONRPCClient {
	if cfg.Binary == "" {
		cfg.Binary = defaultBinary
	}
	if cfg.LogTag == "" {
		cfg.LogTag = "codex-rpc"
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = rpcDefaultRequestTimeout
	}
	if cfg.Logger == nil {
		cfg.Logger = obslog.Bg()
	}
	return &JSONRPCClient{
		cfg:               cfg,
		pending:           make(map[string]*pendingRequest),
		notifHandlers:     make(map[string][]NotificationHandler),
		serverReqHandlers: make(map[string]ServerRequestHandler),
		doneCh:            make(chan struct{}),
	}
}

// Start spawns the child, completes the JSON-RPC `initialize` handshake,
// and returns the server's InitializeResult.
//
// Three failure paths:
//
//   - exec.LookPath / Start failure → returns the spawn error verbatim
//   - initialize timeout → kills the child, returns context.DeadlineExceeded
//   - JSON-RPC error on initialize → kills the child, returns the error
func (c *JSONRPCClient) Start(ctx context.Context, init InitializeParams) (InitializeResult, error) {
	args := append([]string{}, c.cfg.ExtraArgs...)
	args = append(args, "app-server", "--stdio")
	for _, f := range c.cfg.EnableFeatures {
		args = append(args, "--enable", f)
	}
	for _, f := range c.cfg.DisableFeatures {
		args = append(args, "--disable", f)
	}

	cmd := exec.CommandContext(ctx, c.cfg.Binary, args...)
	cmd.Dir = c.cfg.Cwd
	if len(c.cfg.Env) > 0 {
		cmd.Env = append([]string{}, c.cfg.Env...)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return InitializeResult{}, fmt.Errorf("codex rpc: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return InitializeResult{}, fmt.Errorf("codex rpc: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return InitializeResult{}, fmt.Errorf("codex rpc: stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return InitializeResult{}, fmt.Errorf("codex rpc: spawn %q: %w", c.cfg.Binary, err)
	}

	c.cmd = cmd
	c.stdin = stdin
	c.stdout = stdout
	c.stderr = stderr
	c.mu.Lock()
	c.alive = true
	c.mu.Unlock()

	go c.readStdoutLoop()
	go c.pumpStderr()
	go c.waitChild()

	initCtx, cancel := context.WithTimeout(ctx, rpcInitTimeout)
	defer cancel()
	rawResult, err := c.Request(initCtx, "initialize", init)
	if err != nil {
		_ = c.Close()
		return InitializeResult{}, fmt.Errorf("codex rpc: initialize: %w", err)
	}
	var result InitializeResult
	if len(rawResult) > 0 {
		if err := json.Unmarshal(rawResult, &result); err != nil {
			_ = c.Close()
			return InitializeResult{}, fmt.Errorf("codex rpc: decode initialize result: %w", err)
		}
	}
	c.cfg.Logger.Info("codex rpc initialized",
		"tag", c.cfg.LogTag, "user_agent", result.UserAgent, "codex_home", result.CodexHome)
	return result, nil
}

// Alive reports whether the child is still running and stdin is open.
func (c *JSONRPCClient) Alive() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.alive
}

// Close kills the child if still running and rejects every outstanding
// pending request. Safe to call multiple times.
func (c *JSONRPCClient) Close() error {
	var closeErr error
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.alive = false
		c.mu.Unlock()
		if c.stdin != nil {
			_ = c.stdin.Close()
		}
		if c.cmd != nil && c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
		closeErr = c.drainPending(errors.New("codex rpc: client closed"))
	})
	return closeErr
}

// Done is closed when the child exits. Use to coordinate teardown.
func (c *JSONRPCClient) Done() <-chan struct{} {
	return c.doneCh
}

// Request sends a JSON-RPC call and blocks until the response arrives,
// the deadline fires, or the child exits. Returns the raw result JSON
// so the caller can pick its decode shape.
func (c *JSONRPCClient) Request(ctx context.Context, method string, params any) (json.RawMessage, error) {
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
	if err := c.writeFrame(frame); err != nil {
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return nil, fmt.Errorf("codex rpc: write %s: %w", method, err)
	}

	timer := time.NewTimer(c.cfg.RequestTimeout)
	pending.timer = timer
	defer timer.Stop()

	select {
	case r := <-pending.resp:
		return r.result, r.err
	case <-timer.C:
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return nil, fmt.Errorf("codex rpc: %s timed out after %s", method, c.cfg.RequestTimeout)
	case <-ctx.Done():
		c.pendingMu.Lock()
		delete(c.pending, id)
		c.pendingMu.Unlock()
		return nil, ctx.Err()
	}
}

// Notify is fire-and-forget: no id, no reply.
func (c *JSONRPCClient) Notify(method string, params any) error {
	if !c.Alive() {
		return errors.New("codex rpc: client not alive")
	}
	return c.writeFrame(JsonRpcRequest{JsonRpc: JsonRpcVersion, Method: method, Params: params})
}

// SendServerReply is called by a ServerRequestHandler that returned
// DeferReply to send the response later.
func (c *JSONRPCClient) SendServerReply(id any, result any) error {
	return c.writeFrame(JsonRpcResponse{JsonRpc: JsonRpcVersion, ID: id, Result: result})
}

// SendServerError sends an error reply for a deferred server request.
func (c *JSONRPCClient) SendServerError(id any, code int, message string, data any) error {
	return c.writeFrame(JsonRpcResponse{
		JsonRpc: JsonRpcVersion,
		ID:      id,
		Error:   &JsonRpcError{Code: code, Message: message, Data: data},
	})
}

// OnNotification registers h for inbound notifications of method. Multiple
// handlers may register against the same method; all are invoked in
// registration order.
func (c *JSONRPCClient) OnNotification(method string, h NotificationHandler) {
	c.handlersMu.Lock()
	defer c.handlersMu.Unlock()
	c.notifHandlers[method] = append(c.notifHandlers[method], h)
}

// OnAnyNotification registers a fallback handler. Used by session.go for
// debug logging of unmodeled methods.
func (c *JSONRPCClient) OnAnyNotification(h NotificationHandler) {
	c.handlersMu.Lock()
	defer c.handlersMu.Unlock()
	c.anyNotifHandler = h
}

// OnServerRequest registers h for inbound server requests with method.
// Only one handler per method is permitted; later Registers override.
func (c *JSONRPCClient) OnServerRequest(method string, h ServerRequestHandler) {
	c.handlersMu.Lock()
	defer c.handlersMu.Unlock()
	c.serverReqHandlers[method] = h
}

// ---------------------------------------------------------------------------
// internals
// ---------------------------------------------------------------------------

func (c *JSONRPCClient) writeFrame(frame any) error {
	body, err := json.Marshal(frame)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	body = append(body, '\n')
	c.mu.Lock()
	stdin := c.stdin
	alive := c.alive
	c.mu.Unlock()
	if !alive || stdin == nil {
		return errors.New("codex rpc: client not alive")
	}
	if _, err := stdin.Write(body); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	return nil
}

func (c *JSONRPCClient) readStdoutLoop() {
	sc := bufio.NewScanner(c.stdout)
	sc.Buffer(make([]byte, 0, 64*1024), rpcStdoutBufferMax)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		c.dispatchFrame(line)
	}
	if err := sc.Err(); err != nil && !errors.Is(err, io.EOF) {
		c.cfg.Logger.Warn("codex rpc stdout scan err", "tag", c.cfg.LogTag, "err", err)
	}
}

// dispatchFrame classifies an inbound frame and routes it.
//
// Inbound shapes:
//
//	{"id": ..., "result": ...}    → response (success)
//	{"id": ..., "error":  ...}    → response (failure)
//	{"id": ..., "method": ...}    → server request
//	{"method": ..., "params": ..} → notification (no id)
func (c *JSONRPCClient) dispatchFrame(line []byte) {
	var probe struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(line, &probe); err != nil {
		c.cfg.Logger.Warn("codex rpc non-json frame",
			"tag", c.cfg.LogTag, "preview", string(truncate(line, 200)))
		return
	}
	hasID := len(probe.ID) > 0 && string(probe.ID) != "null"
	hasMethod := probe.Method != ""
	hasResp := len(probe.Result) > 0 || len(probe.Error) > 0

	switch {
	case hasID && hasResp:
		c.handleResponse(probe.ID, probe.Result, probe.Error)
	case hasID && hasMethod:
		c.handleServerRequest(line, probe.ID, probe.Method)
	case hasMethod:
		c.handleNotification(probe.Method, line)
	default:
		c.cfg.Logger.Warn("codex rpc unrecognised frame", "tag", c.cfg.LogTag, "preview", string(truncate(line, 200)))
	}
}

func (c *JSONRPCClient) handleResponse(rawID, rawResult, rawError json.RawMessage) {
	var id string
	if err := json.Unmarshal(rawID, &id); err != nil {
		// id may be a number on some replies; we only ever generate
		// hex string ids ourselves, so a numeric reply is orphan.
		c.cfg.Logger.Warn("codex rpc response with non-string id", "tag", c.cfg.LogTag, "raw_id", string(rawID))
		return
	}
	c.pendingMu.Lock()
	p, ok := c.pending[id]
	if ok {
		delete(c.pending, id)
	}
	c.pendingMu.Unlock()
	if !ok {
		c.cfg.Logger.Warn("codex rpc orphan response", "tag", c.cfg.LogTag, "id", id)
		return
	}
	if p.timer != nil {
		p.timer.Stop()
	}
	if len(rawError) > 0 && string(rawError) != "null" {
		var errBody JsonRpcError
		if err := json.Unmarshal(rawError, &errBody); err != nil {
			p.resp <- rpcResponse{err: fmt.Errorf("codex rpc: malformed error reply on %s: %w", p.method, err)}
			return
		}
		p.resp <- rpcResponse{err: fmt.Errorf("codex rpc: %s: %d %s", p.method, errBody.Code, errBody.Message)}
		return
	}
	p.resp <- rpcResponse{result: rawResult}
}

func (c *JSONRPCClient) handleNotification(method string, rawFrame []byte) {
	var env struct {
		Params json.RawMessage `json:"params"`
	}
	_ = json.Unmarshal(rawFrame, &env)
	c.handlersMu.RLock()
	handlers := append([]NotificationHandler{}, c.notifHandlers[method]...)
	any := c.anyNotifHandler
	c.handlersMu.RUnlock()
	if len(handlers) == 0 && any == nil {
		c.cfg.Logger.Debug("codex rpc unhandled notification", "tag", c.cfg.LogTag, "method", method)
		return
	}
	for _, h := range handlers {
		safeInvoke(h, env.Params, c.cfg.Logger, c.cfg.LogTag, "notification "+method)
	}
	if any != nil {
		safeInvoke(any, env.Params, c.cfg.Logger, c.cfg.LogTag, "notification "+method)
	}
}

func (c *JSONRPCClient) handleServerRequest(rawFrame []byte, rawID json.RawMessage, method string) {
	var env struct {
		Params json.RawMessage `json:"params"`
	}
	_ = json.Unmarshal(rawFrame, &env)
	var id any
	_ = json.Unmarshal(rawID, &id)

	c.handlersMu.RLock()
	h, ok := c.serverReqHandlers[method]
	c.handlersMu.RUnlock()
	if !ok {
		// Method-not-found per JSON-RPC 2.0 — codex hangs if we drop it.
		_ = c.SendServerError(id, -32601, fmt.Sprintf("no handler for %s", method), nil)
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				_ = c.SendServerError(id, -32603, fmt.Sprintf("panic in %s handler: %v", method, r), nil)
			}
		}()
		result, err := h(env.Params, id)
		if err != nil {
			_ = c.SendServerError(id, -32603, err.Error(), nil)
			return
		}
		if _, deferred := result.(deferReplySentinel); deferred {
			return
		}
		if err := c.SendServerReply(id, result); err != nil {
			c.cfg.Logger.Warn("codex rpc reply failed", "tag", c.cfg.LogTag, "method", method, "err", err)
		}
	}()
}

func (c *JSONRPCClient) pumpStderr() {
	sc := bufio.NewScanner(c.stderr)
	sc.Buffer(make([]byte, 0, 16*1024), 1<<20)
	for sc.Scan() {
		c.cfg.Logger.Warn("codex stderr", "tag", c.cfg.LogTag, "line", sc.Text())
	}
}

func (c *JSONRPCClient) waitChild() {
	defer close(c.doneCh)
	err := c.cmd.Wait()
	c.mu.Lock()
	c.alive = false
	if c.cmd.ProcessState != nil {
		code := c.cmd.ProcessState.ExitCode()
		c.exitCode = &code
	}
	c.mu.Unlock()
	_ = c.drainPending(fmt.Errorf("codex app-server exited: %v", err))
}

func (c *JSONRPCClient) drainPending(cause error) error {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	for id, p := range c.pending {
		if p.timer != nil {
			p.timer.Stop()
		}
		// non-blocking send — resp channel is buffered=1.
		select {
		case p.resp <- rpcResponse{err: cause}:
		default:
		}
		delete(c.pending, id)
	}
	return nil
}

func safeInvoke(h NotificationHandler, params json.RawMessage, log *slog.Logger, tag, where string) {
	defer func() {
		if r := recover(); r != nil {
			log.Warn("codex rpc handler panic", "tag", tag, "where", where, "panic", fmt.Sprintf("%v", r))
		}
	}()
	h(params)
}

func newRequestID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func truncate(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	return b[:n]
}
