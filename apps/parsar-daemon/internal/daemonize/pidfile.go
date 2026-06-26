package daemonize

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ErrStaleOrCorrupt is returned by ReadPIDFile when the file exists
// but its contents don't look like a live PID. Wraps the underlying
// parse error or syscall.ESRCH for errors.Is.
var ErrStaleOrCorrupt = errors.New("daemonize: pidfile stale or corrupt")

// ErrNotRunning is returned by IsAlive when the PID has no process.
var ErrNotRunning = errors.New("daemonize: process not running")

// WritePIDFile writes pid to path atomically. The temp file lives in
// the same directory so the rename is on the same filesystem
// (required by os.Rename for atomicity). 0o600 matches the profile
// dir's privacy.
func WritePIDFile(path string, pid int) error {
	if path == "" {
		return errors.New("daemonize.WritePIDFile: empty path")
	}
	if pid <= 0 {
		return fmt.Errorf("daemonize.WritePIDFile: invalid pid %d", pid)
	}
	tmp := path + ".tmp"
	body := []byte(strconv.Itoa(pid) + "\n")
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return fmt.Errorf("daemonize: write tmp pidfile: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("daemonize: rename pidfile: %w", err)
	}
	return nil
}

// ReadPIDFile reads, parses, and liveness-checks the pidfile.
// Missing file returns os.ErrNotExist verbatim; a file whose pid has
// no live process returns ErrStaleOrCorrupt wrapping ErrNotRunning.
func ReadPIDFile(path string) (int, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	pid, parseErr := strconv.Atoi(strings.TrimSpace(string(raw)))
	if parseErr != nil || pid <= 0 {
		return 0, fmt.Errorf("%w: %q", ErrStaleOrCorrupt, strings.TrimSpace(string(raw)))
	}
	if err := IsAlive(pid); err != nil {
		return pid, fmt.Errorf("%w: pid=%d: %w", ErrStaleOrCorrupt, pid, err)
	}
	return pid, nil
}

// IsAlive returns nil if pid corresponds to a process this user can
// signal. Wraps ErrNotRunning when the process is gone; other errors
// (e.g. EPERM) returned verbatim.
func IsAlive(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("daemonize.IsAlive: invalid pid %d", pid)
	}
	// signal 0 is the POSIX "does this process exist?" probe:
	//   nil   → exists, signalable
	//   ESRCH → no such process
	//   EPERM → exists but owned by another user
	if err := syscall.Kill(pid, syscall.Signal(0)); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return fmt.Errorf("%w (pid=%d)", ErrNotRunning, pid)
		}
		return err
	}
	return nil
}

// RemovePIDFile deletes the pidfile. Missing files aren't an error so
// `parsar-daemon stop` is idempotent.
func RemovePIDFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("daemonize: remove pidfile: %w", err)
	}
	return nil
}

// SignalAndWait SIGTERMs pid, polls for exit on 100ms cadence up to
// timeout, then SIGKILLs if still alive. Caller is responsible for
// removing the pidfile after success.
func SignalAndWait(pid int, timeout time.Duration) error {
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return fmt.Errorf("daemonize: SIGTERM pid=%d: %w", pid, err)
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := IsAlive(pid); err != nil {
			if errors.Is(err, ErrNotRunning) {
				return nil
			}
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}

	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return fmt.Errorf("daemonize: SIGKILL pid=%d: %w", pid, err)
	}
	// Grace window for the kernel to reap before any subsequent
	// IsAlive call.
	time.Sleep(50 * time.Millisecond)
	return nil
}
