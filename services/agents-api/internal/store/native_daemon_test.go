package store_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func nativeDispatchHarness(t *testing.T) (*dispatchHarness, context.Context, string) {
	t.Helper()
	binary, root := os.Getenv("PARSAR_NATIVE_DAEMON_BIN"), os.Getenv("PARSAR_NATIVE_PROOF_DIR")
	if binary == "" || root == "" {
		t.Skip("explicit native daemon binary and evidence directory required")
	}
	h := newDispatchHarness(t)
	oldPeer, _ := h.registry.LookupDevice(h.device.ID)
	h.conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)
	home, err := os.MkdirTemp(root, "execution-native-")
	if err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(home, "parsar-daemon", "execution")
	if err = os.MkdirAll(profile, 0700); err != nil {
		t.Fatal(err)
	}
	auth, _ := json.Marshal(map[string]string{"server_url": h.url + "/api/v1", "runtime_id": h.device.ID, "runner_credential": h.credential, "device_name": "native proof"})
	if err = os.WriteFile(filepath.Join(profile, "auth.json"), auth, 0600); err != nil {
		t.Fatal(err)
	}
	daemonLog, err := os.Create(filepath.Join(home, "daemon.log"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = daemonLog.Close() })
	cmd := exec.Command(binary, "connect", "--profile", "execution")
	cmd.Env = append(os.Environ(), "PARSAR_HOME="+home)
	cmd.Stdout, cmd.Stderr = daemonLog, daemonLog
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := make(chan error, 1)
	go func() { stopped <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case <-stopped:
		case <-time.After(8 * time.Second):
			_ = cmd.Process.Kill()
			<-stopped
		}
	})
	deadline := time.Now().Add(20 * time.Second)
	for {
		peer, e := h.registry.LookupDevice(h.device.ID)
		if e == nil && peer != oldPeer {
			if info, found, known := peer.AgentKindStatus("codex"); known && found && info.Available && info.Capabilities.EnvironmentNone {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("native daemon not ready; logs %s", home)
		}
		time.Sleep(50 * time.Millisecond)
	}
	return h, ctx, home
}
