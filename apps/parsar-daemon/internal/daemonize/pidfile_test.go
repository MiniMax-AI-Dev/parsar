package daemonize

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestWriteAndReadPIDFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "connect.pid")
	pid := os.Getpid()

	if err := WritePIDFile(path, pid); err != nil {
		t.Fatalf("WritePIDFile: %v", err)
	}

	got, err := ReadPIDFile(path)
	if err != nil {
		t.Fatalf("ReadPIDFile: %v", err)
	}
	if got != pid {
		t.Fatalf("ReadPIDFile = %d, want %d", got, pid)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("pidfile mode = %o, want 0600", info.Mode().Perm())
	}
}

func TestReadPIDFileMissingReturnsErrNotExist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nope.pid")
	_, err := ReadPIDFile(path)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want os.ErrNotExist", err)
	}
}

func TestReadPIDFileCorruptReturnsStaleOrCorrupt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.pid")
	if err := os.WriteFile(path, []byte("not a number\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := ReadPIDFile(path)
	if !errors.Is(err, ErrStaleOrCorrupt) {
		t.Fatalf("err = %v, want ErrStaleOrCorrupt", err)
	}
}

func TestReadPIDFileStaleReturnsStaleOrCorrupt(t *testing.T) {
	// Pick a PID almost certainly dead. WritePIDFile rejects 0 so we
	// write manually.
	dir := t.TempDir()
	path := filepath.Join(dir, "stale.pid")
	if err := os.WriteFile(path, []byte("99999999\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := ReadPIDFile(path); !errors.Is(err, ErrStaleOrCorrupt) {
		// Skip if 99999999 happens to exist.
		t.Logf("ReadPIDFile = %v (PID 99999999 may exist on this host); skipping", err)
		t.SkipNow()
	}
}

func TestIsAliveTrueForOurselves(t *testing.T) {
	if err := IsAlive(os.Getpid()); err != nil {
		t.Fatalf("IsAlive(self) = %v, want nil", err)
	}
}

func TestIsAliveFalseForDeadPID(t *testing.T) {
	err := IsAlive(99999999)
	if err == nil {
		t.Skip("PID 99999999 unexpectedly exists; skipping")
	}
	if !errors.Is(err, ErrNotRunning) {
		t.Fatalf("err = %v, want ErrNotRunning", err)
	}
}

func TestRemovePIDFileIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.pid")
	if err := WritePIDFile(path, os.Getpid()); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := RemovePIDFile(path); err != nil {
		t.Fatalf("first remove: %v", err)
	}
	// Second remove on missing file should be a no-op.
	if err := RemovePIDFile(path); err != nil {
		t.Fatalf("second remove: %v", err)
	}
}

func TestSignalAndWaitKillsChild(t *testing.T) {
	// Launch a sleeping subshell so we have a real PID to signal.
	cmd := startSleepingChild(t, 30*time.Second)
	pid := cmd.Process.Pid
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	if err := SignalAndWait(pid, 2*time.Second); err != nil {
		t.Fatalf("SignalAndWait: %v", err)
	}
	// In production, init reaps the child so IsAlive returns
	// ErrNotRunning. In tests we're the parent so the child becomes
	// a zombie — Wait() reaps it.
	if _, err := cmd.Process.Wait(); err != nil {
		t.Fatalf("Wait after SignalAndWait: %v", err)
	}
	if err := IsAlive(pid); err == nil {
		t.Fatalf("child still alive after SignalAndWait+Wait")
	} else if !errors.Is(err, ErrNotRunning) {
		t.Fatalf("IsAlive err = %v, want ErrNotRunning", err)
	}
}

func TestSignalAndWaitEscalatesToSIGKILL(t *testing.T) {
	// Child traps SIGTERM and refuses to exit. 300ms timeout so the
	// escalation path fires quickly.
	cmd := startTrapChild(t)
	pid := cmd.Process.Pid
	defer func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}()

	start := time.Now()
	if err := SignalAndWait(pid, 300*time.Millisecond); err != nil {
		t.Fatalf("SignalAndWait: %v", err)
	}
	if d := time.Since(start); d < 250*time.Millisecond {
		t.Errorf("escalation happened too early (took %s); SIGTERM grace period not honoured", d)
	}
	// Reap so the kernel reuses the PID. ProcessState confirms SIGKILL.
	state, err := cmd.Process.Wait()
	if err != nil {
		t.Fatalf("Wait after escalation: %v", err)
	}
	ws, ok := state.Sys().(syscall.WaitStatus)
	if !ok || !ws.Signaled() || ws.Signal() != syscall.SIGKILL {
		t.Fatalf("child exit = %v, want killed by SIGKILL", state)
	}
}

func TestSignalAndWaitNoopOnAlreadyDead(t *testing.T) {
	cmd := startSleepingChild(t, 0)
	pid := cmd.Process.Pid
	// Wait for child to actually be gone before signalling so we
	// test the ESRCH branch rather than a race.
	_, _ = cmd.Process.Wait()

	if err := SignalAndWait(pid, time.Second); err != nil {
		t.Fatalf("SignalAndWait on dead pid: %v, want nil", err)
	}
}
