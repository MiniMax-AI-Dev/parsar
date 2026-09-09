package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestJSONRPCClientCloseReapsChildProcess(t *testing.T) {
	cfg := JSONRPCConfig{
		Binary:         os.Args[0],
		ExtraArgs:      []string{"-test.run=TestJSONRPCClientFakeCodexProcess", "--"},
		Env:            append(os.Environ(), "CODEX_RPC_FAKE_PROCESS=1"),
		LogTag:         "codex-rpc-process-test",
		RequestTimeout: 2 * time.Second,
	}
	client := NewJSONRPCClient(cfg)

	_, err := client.Start(context.Background(), InitializeParams{
		ClientInfo: InitializeClientInfo{Name: "test", Version: "0"},
	})
	if err != nil {
		t.Fatalf("start fake process: %v", err)
	}
	if client.cmd == nil || client.cmd.Process == nil {
		t.Fatal("client did not spawn a child process")
	}

	if err := client.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	select {
	case <-client.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("child process was not reaped")
	}
	if client.cmd.ProcessState == nil {
		t.Fatalf("process state not exited after close: %#v", client.cmd.ProcessState)
	}
}

func TestJSONRPCClientResponseTimeoutStartsAfterWrite(t *testing.T) {
	client, server, cleanup := NewTestClient()
	defer cleanup()
	client.cfg.RequestTimeout = 100 * time.Millisecond
	responseDone := make(chan error, 1)
	go func() {
		// Block the pipe write for longer than the response timeout.
		time.Sleep(200 * time.Millisecond)
		var request JsonRpcRequest
		if err := json.NewDecoder(server.FromClient).Decode(&request); err != nil {
			responseDone <- err
			return
		}
		time.Sleep(10 * time.Millisecond)
		responseDone <- json.NewEncoder(server.ToClient).Encode(JsonRpcResponse{
			JsonRpc: JsonRpcVersion, ID: request.ID, Result: "ok",
		})
	}()
	result, err := client.Request(context.Background(), "echo", nil)
	if responseErr := <-responseDone; responseErr != nil {
		t.Fatalf("send response: %v", responseErr)
	}
	if err != nil || string(result) != `"ok"` {
		t.Fatalf("response after blocked write: result=%s error=%v", result, err)
	}
}

func TestJSONRPCClientFakeCodexProcess(t *testing.T) {
	if os.Getenv("CODEX_RPC_FAKE_PROCESS") != "1" {
		return
	}
	line, err := bufio.NewReader(os.Stdin).ReadBytes('\n')
	if err != nil {
		fmt.Fprintf(os.Stderr, "read initialize: %v\n", err)
		os.Exit(2)
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(line, &req); err != nil {
		fmt.Fprintf(os.Stderr, "decode initialize: %v\n", err)
		os.Exit(2)
	}
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      req.ID,
		"result": map[string]any{
			"userAgent": "fake-codex",
			"codexHome": "/tmp/fake-codex-home",
		},
	}
	body, _ := json.Marshal(resp)
	fmt.Printf("%s\n", body)
	for {
		time.Sleep(time.Second)
	}
}
