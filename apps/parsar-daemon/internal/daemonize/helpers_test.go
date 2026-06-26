package daemonize

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// spawnTestChildEnv flips the test binary into "child" mode for fork
// tests: TestMain writes a marker, blocks until SIGTERM/SIGINT, exits.
// Lets Spawn re-exec the test binary as the child.
const spawnTestChildEnv = "PARSAR_DAEMON_SPAWN_TEST_CHILD"

func TestMain(m *testing.M) {
	if os.Getenv(spawnTestChildEnv) == "1" {
		runSpawnTestChild()
		return
	}
	os.Exit(m.Run())
}

func runSpawnTestChild() {
	fmt.Fprintln(os.Stdout, "spawn-test-child:stdout")
	fmt.Fprintln(os.Stderr, "spawn-test-child:stderr")

	// Honour SIGTERM so the parent test can clean us up. Fallback
	// deadline so a stranded child doesn't survive a crashing test.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	select {
	case <-sigCh:
	case <-time.After(15 * time.Second):
	}
	os.Exit(0)
}

// startSleepingChild launches a child that sleeps for dur and exits.
// dur=0 → `sh -c true` for testing "signal a process that's already
// gone" paths. sh because we need a real kernel PID with /proc entry.
func startSleepingChild(t *testing.T, dur time.Duration) *exec.Cmd {
	t.Helper()
	var cmd *exec.Cmd
	if dur <= 0 {
		cmd = exec.Command("sh", "-c", "true")
	} else {
		// Convert to whole seconds when possible, sub-second via sleep
		// arg with decimal (Linux/macOS sh both accept "sleep 0.05").
		secs := dur.Seconds()
		cmd = exec.Command("sh", "-c", "sleep "+strconv.FormatFloat(secs, 'f', 3, 64))
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("startSleepingChild: %v", err)
	}
	return cmd
}

// startTrapChild launches a child that ignores SIGTERM, forcing the
// SIGKILL escalation path. Waits for READY on stdout so a fast caller
// doesn't race the trap install.
func startTrapChild(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sh", "-c", "trap '' TERM; echo READY; sleep 30")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("startTrapChild StdoutPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("startTrapChild Start: %v", err)
	}
	// Read up to READY with a hard deadline so a broken shell can't
	// hang the test.
	readyCh := make(chan error, 1)
	go func() {
		buf := make([]byte, 16)
		var got []byte
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				got = append(got, buf[:n]...)
				if len(got) >= 5 && string(got[:5]) == "READY" {
					readyCh <- nil
					return
				}
			}
			if err != nil {
				readyCh <- err
				return
			}
		}
	}()
	select {
	case err := <-readyCh:
		if err != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
			t.Fatalf("startTrapChild waiting READY: %v", err)
		}
	case <-time.After(2 * time.Second):
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
		t.Fatalf("startTrapChild: timed out waiting for READY")
	}
	return cmd
}
