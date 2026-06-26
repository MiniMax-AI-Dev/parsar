package daemonize

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSpawnReExecsWithSentinelAndPIDFile(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "child.log")
	pidPath := filepath.Join(dir, "child.pid")

	// argv[0] is ignored (Spawn uses os.Executable()); argv[1:]
	// becomes child args. Placeholder subcommand so flag parsing
	// wouldn't choke — runSpawnTestChild short-circuits anyway.
	pid, err := Spawn(
		[]string{"parsar-daemon", "child-mode"},
		ReExecOptions{
			LogPath:  logPath,
			PIDPath:  pidPath,
			ExtraEnv: []string{spawnTestChildEnv + "=1"},
		},
	)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer func() {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}()

	if pid <= 0 {
		t.Fatalf("Spawn returned non-positive pid %d", pid)
	}

	gotPID, err := ReadPIDFile(pidPath)
	if err != nil {
		t.Fatalf("ReadPIDFile: %v", err)
	}
	if gotPID != pid {
		t.Fatalf("pidfile pid = %d, want %d", gotPID, pid)
	}

	// Poll for log contents — child writes markers immediately but
	// stdio is kernel-buffered.
	deadline := time.Now().Add(3 * time.Second)
	var logBody []byte
	for time.Now().Before(deadline) {
		logBody, err = os.ReadFile(logPath)
		if err == nil && strings.Contains(string(logBody), "spawn-test-child:stdout") &&
			strings.Contains(string(logBody), "spawn-test-child:stderr") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(string(logBody), "spawn-test-child:stdout") {
		t.Errorf("log missing stdout marker; got %q", string(logBody))
	}
	if !strings.Contains(string(logBody), "spawn-test-child:stderr") {
		t.Errorf("log missing stderr marker; got %q", string(logBody))
	}

	// SignalAndWait should now drop the child cleanly.
	if err := SignalAndWait(pid, 2*time.Second); err != nil {
		t.Fatalf("SignalAndWait: %v", err)
	}
}

func TestSpawnRejectsEmptyArgv(t *testing.T) {
	_, err := Spawn(nil, ReExecOptions{LogPath: "/tmp/x", PIDPath: "/tmp/y"})
	if err == nil {
		t.Fatalf("Spawn(nil) succeeded; want error")
	}
}

func TestSpawnRejectsMissingPaths(t *testing.T) {
	_, err := Spawn([]string{"parsar-daemon"}, ReExecOptions{})
	if err == nil {
		t.Fatalf("Spawn(no paths) succeeded; want error")
	}
}

func TestIsBackgroundChildHonoursEnv(t *testing.T) {
	t.Setenv(BackgroundSentinelEnv, "1")
	if !IsBackgroundChild() {
		t.Fatalf("IsBackgroundChild = false with env=1, want true")
	}
	t.Setenv(BackgroundSentinelEnv, "")
	if IsBackgroundChild() {
		t.Fatalf("IsBackgroundChild = true with env unset, want false")
	}
}
