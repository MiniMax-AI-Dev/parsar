package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func startPublicNativeProcess(t *testing.T, ctx context.Context, root, name string, environment []string, binary string, args ...string) *relayProcess {
	t.Helper()
	output, err := os.OpenFile(filepath.Join(root, name+".log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, binary, args...)
	command.Env, command.Dir = environment, root
	command.Stdout, command.Stderr = output, output
	if err := command.Start(); err != nil {
		_ = output.Close()
		t.Fatal(name, "failed to start", err)
	}
	process := &relayProcess{command: command, done: make(chan struct{})}
	go func() { process.err = command.Wait(); _ = output.Close(); close(process.done) }()
	t.Cleanup(func() {
		_ = command.Process.Signal(os.Interrupt)
		select {
		case <-process.done:
		case <-time.After(12 * time.Second):
			_ = command.Process.Kill()
			<-process.done
		}
	})
	return process
}

func publicNativeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func awaitPublicNativeServer(t *testing.T, ctx context.Context, process *relayProcess, base string) {
	t.Helper()
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{Proxy: nil}}
	defer client.CloseIdleConnections()
	awaitDaemonRemoteCondition(t, ctx, 20*time.Second, "built Agents API health", func() bool {
		select {
		case <-process.done:
			t.Fatal("built Agents API exited; inspect private server log")
		default:
		}
		response, err := client.Get(base + "/healthz")
		if err != nil {
			return false
		}
		_ = response.Body.Close()
		return response.StatusCode == http.StatusOK
	})
}

func writePublicNativeJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func startPublicNativeClient(t *testing.T, ctx context.Context, root string, settings map[string]string) *preparedPublicObserver {
	t.Helper()
	directory := filepath.Join(root, "public-environment")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	settings["evidence"] = directory
	input, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	output, err := os.OpenFile(filepath.Join(directory, "observer.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		t.Fatal(err)
	}
	owner, cancel := context.WithCancel(ctx)
	command := exec.CommandContext(owner, os.Getenv("PARSAR_OFFICIAL_SDK_PYTHON"), "../../tests/official_self_hosted.py")
	command.Stdin, command.Stdout, command.Stderr = bytes.NewReader(input), output, output
	if err := command.Start(); err != nil {
		cancel()
		_ = output.Close()
		t.Fatal(err)
	}
	observer := &preparedPublicObserver{directory: directory, command: command, cancel: cancel, done: make(chan struct{})}
	go func() { observer.err = command.Wait(); _ = output.Close(); close(observer.done) }()
	t.Cleanup(observer.close)
	return observer
}

func awaitPublicNativeSignal(t *testing.T, ctx context.Context, observer *preparedPublicObserver, name string, timeout time.Duration) map[string]string {
	t.Helper()
	var value map[string]string
	awaitDaemonRemoteCondition(t, ctx, timeout, "public client "+name, func() bool {
		select {
		case <-observer.done:
			t.Fatal("public client exited before signal; inspect private client log", observer.err)
		default:
		}
		data, err := os.ReadFile(filepath.Join(observer.directory, name+".json"))
		if os.IsNotExist(err) {
			return false
		}
		if err != nil || json.Unmarshal(data, &value) != nil {
			t.Fatal("invalid public client signal")
		}
		return true
	})
	return value
}

func publicNativeEnvironment(overrides map[string]string) []string {
	result := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if _, replaced := overrides[name]; !replaced {
			result = append(result, entry)
		}
	}
	for name, value := range overrides {
		result = append(result, name+"="+value)
	}
	return result
}
