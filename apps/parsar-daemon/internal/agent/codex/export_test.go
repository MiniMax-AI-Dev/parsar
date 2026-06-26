package codex

import (
	"context"
	"encoding/json"
	"io"
)

// TestClient is a JSONRPCClient stand-in driven by in-memory pipes.
// Lets unit tests exercise the wire dispatch (response / notification /
// server-request branches) without spawning a real codex binary.
//
// Returned from NewTestClient as a triple:
//
//	(client, ServerSide, cleanup)
//
// ServerSide is the codex-side view: Read() returns what the daemon
// wrote (outgoing frames), Write() injects what codex would have sent.
// cleanup tears down both halves so the test can defer it.
type TestClient struct {
	*JSONRPCClient
	serverIn  *io.PipeReader
	serverOut *io.PipeWriter
	clientIn  *io.PipeReader
	clientOut *io.PipeWriter
}

// ServerSide is the half of the pipe pair the test owns.
type ServerSide struct {
	// FromClient reads frames the JSONRPCClient sent (what would have
	// gone to codex's stdin).
	FromClient *io.PipeReader
	// ToClient writes frames the test wants the JSONRPCClient to receive
	// (what would have come from codex's stdout).
	ToClient *io.PipeWriter
}

// NewTestClient builds a JSONRPCClient wired to a pair of in-memory pipes.
// The returned cleanup func closes everything; defer it.
func NewTestClient() (*TestClient, ServerSide, func()) {
	daemonStdinR, daemonStdinW := io.Pipe()
	daemonStdoutR, daemonStdoutW := io.Pipe()

	cfg := JSONRPCConfig{LogTag: "codex-rpc-test"}
	c := NewJSONRPCClient(cfg)
	c.stdin = daemonStdinW
	c.stdout = daemonStdoutR
	c.stderr = io.NopCloser(emptyReader{})
	c.mu.Lock()
	c.alive = true
	c.mu.Unlock()
	go c.readStdoutLoop()

	tc := &TestClient{
		JSONRPCClient: c,
		serverIn:      daemonStdinR,
		serverOut:     daemonStdoutW,
		clientIn:      daemonStdoutR,
		clientOut:     daemonStdinW,
	}
	cleanup := func() {
		_ = daemonStdinW.Close()
		_ = daemonStdinR.Close()
		_ = daemonStdoutW.Close()
		_ = daemonStdoutR.Close()
		c.mu.Lock()
		c.alive = false
		c.mu.Unlock()
	}
	return tc, ServerSide{FromClient: daemonStdinR, ToClient: daemonStdoutW}, cleanup
}

type emptyReader struct{}

func (emptyReader) Read(_ []byte) (int, error) { return 0, io.EOF }

// SendCannedResponse reads one outgoing frame from the client, then
// writes a {"id":<id>,"result":<result>} reply. Returns the method the
// client sent (for assertions). ctx reserved for future cancellation.
func SendCannedResponse(_ context.Context, srv ServerSide, result any) (method string, err error) {
	decoder := json.NewDecoder(srv.FromClient)
	var req struct {
		ID     string `json:"id"`
		Method string `json:"method"`
	}
	if err := decoder.Decode(&req); err != nil {
		return "", err
	}
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      req.ID,
		"result":  result,
	}
	body, _ := json.Marshal(resp)
	body = append(body, '\n')
	_, err = srv.ToClient.Write(body)
	return req.Method, err
}

// SendNotification pushes a notification (no id) to the client.
func SendNotification(srv ServerSide, method string, params any) error {
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
	body = append(body, '\n')
	_, err := srv.ToClient.Write(body)
	return err
}

// SendServerRequest pushes a server-initiated request and returns when
// the frame is on the wire.
func SendServerRequest(srv ServerSide, id string, method string, params any) error {
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	})
	body = append(body, '\n')
	_, err := srv.ToClient.Write(body)
	return err
}
