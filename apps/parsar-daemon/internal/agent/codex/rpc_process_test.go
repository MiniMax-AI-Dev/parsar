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
