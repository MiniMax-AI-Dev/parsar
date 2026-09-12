//go:build linux

package clirunner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestOwnedGroupCancellation(t *testing.T) {
	for _, mode := range []string{"graceful", "ignore-term", "leader-exits"} {
		for _, parentCancel := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/parent=%t", mode, parentCancel), func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				dir := t.TempDir()
				p := startOwnedHelper(t, ctx, mode, dir)
				stdout, stderr := drainOwned(t, p)
				child := waitChild(t, dir)
				group, err := syscall.Getpgid(child)
				if err != nil || group != p.Cmd.Process.Pid {
					t.Fatalf("child group = %d, err = %v", group, err)
				}
				started := time.Now()
				if parentCancel {
					cancel()
				} else {
					p.Cancel()
					p.Cancel()
				}
				<-p.Context().Done()
				awaitOutput(t, stdout)
				awaitOutput(t, stderr)
				if mode == "ignore-term" && time.Since(started) < 300*time.Millisecond {
					t.Fatal("TERM-ignoring processes exited before escalation")
				}
				if time.Since(started) > 3*time.Second {
					t.Fatal("cancellation exceeded bound")
				}
				first := p.Wait()
				if fmt.Sprint(p.Wait()) != fmt.Sprint(first) {
					t.Fatal("Wait result changed")
				}
				p.Cancel()
				waitExited(t, child)
				if mode == "graceful" {
					for _, name := range []string{"child-stopped", "leader-stopped"} {
						if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
							t.Fatalf("graceful shutdown missing %s: %v", name, err)
						}
					}
				}
			})
		}
	}
}

func TestOwnedGroupLeaderExitReleasesInheritedOutput(t *testing.T) {
	dir := t.TempDir()
	p := startOwnedHelper(t, context.Background(), "natural-exit", dir)
	stdout, stderr := drainOwned(t, p)
	child := waitChild(t, dir)
	// Read to EOF before Wait, as the existing adapters do.
	awaitOutput(t, stdout)
	awaitOutput(t, stderr)
	if err := p.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	waitExited(t, child)
}

func TestOwnedGroupPreservesOutputAfterLeaderExit(t *testing.T) {
	p := startOwnedHelper(t, context.Background(), "output", t.TempDir())
	stdout, stderr := drainOwned(t, p)
	for _, stream := range []<-chan []byte{stdout, stderr} {
		actual := awaitOutput(t, stream)
		if !bytes.Equal(actual, bytes.Repeat([]byte("x"), 512*1024)) {
			t.Fatalf("truncated output: %d bytes", len(actual))
		}
	}
	if err := p.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	select {
	case <-p.Done():
	default:
		t.Fatal("Done is not closed")
	}
}

func startOwnedHelper(t *testing.T, ctx context.Context, mode, dir string) *Process {
	t.Helper()
	p, err := Start(StartOptions{
		Parent: ctx, Binary: os.Args[0],
		Args:            []string{"-test.run=^TestOwnedGroupHelper$", "--", "leader", mode, dir},
		Env:             append(os.Environ(), "GO_WANT_OWNED_GROUP=1", "GORACE=atexit_sleep_ms=0"),
		OwnProcessGroup: true, KillTimeout: 300 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Cancel(); _ = p.Wait() })
	return p
}

func drainOwned(t *testing.T, p *Process) (<-chan []byte, <-chan []byte) {
	t.Helper()
	read := func(r io.Reader) <-chan []byte {
		ch := make(chan []byte, 1)
		go func() {
			value, err := io.ReadAll(r)
			if err != nil {
				t.Errorf("read output: %v", err)
			}
			ch <- value
		}()
		return ch
	}
	return read(p.Stdout), read(p.Stderr)
}

func awaitOutput(t *testing.T, ch <-chan []byte) []byte {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("output remained open")
		return nil
	}
}

func waitChild(t *testing.T, dir string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		value, err := os.ReadFile(filepath.Join(dir, "child-ready"))
		if err == nil {
			pid, err := strconv.Atoi(string(value))
			if err == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("child did not start")
	return 0
}

func waitExited(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		value, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if os.IsNotExist(err) {
			return
		}
		if err == nil {
			fields := strings.Fields(string(value)[strings.LastIndex(string(value), ")")+1:])
			if len(fields) > 0 && fields[0] == "Z" {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("child %d is still running", pid)
}

func TestOwnedGroupHelper(t *testing.T) {
	if os.Getenv("GO_WANT_OWNED_GROUP") != "1" {
		return
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) != 4 {
		os.Exit(2)
	}
	role, mode, dir := args[1], args[2], args[3]
	if mode == "output" {
		data := bytes.Repeat([]byte("x"), 512*1024)
		_, _ = os.Stdout.Write(data)
		_, _ = os.Stderr.Write(data)
		os.Exit(0)
	}
	terminated := make(chan os.Signal, 1)
	if mode == "ignore-term" || role == "child" && mode != "graceful" {
		signal.Ignore(syscall.SIGTERM)
	} else {
		signal.Notify(terminated, syscall.SIGTERM)
	}
	if role == "child" {
		_ = os.WriteFile(filepath.Join(dir, "child-ready"), []byte(strconv.Itoa(os.Getpid())), 0o600)
		if mode != "graceful" {
			time.Sleep(time.Hour)
		}
		<-terminated
		_ = os.WriteFile(filepath.Join(dir, "child-stopped"), nil, 0o600)
		os.Exit(0)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestOwnedGroupHelper$", "--", "child", mode, dir)
	child.Env, child.Stdout, child.Stderr = os.Environ(), os.Stdout, os.Stderr
	if err := child.Start(); err != nil {
		os.Exit(3)
	}
	if mode == "natural-exit" {
		for {
			if _, err := os.Stat(filepath.Join(dir, "child-ready")); err == nil {
				os.Exit(0)
			}
			time.Sleep(time.Millisecond)
		}
	}
	if mode == "ignore-term" {
		time.Sleep(time.Hour)
	}
	<-terminated
	if mode == "leader-exits" {
		os.Exit(0)
	}
	_ = child.Wait()
	_ = os.WriteFile(filepath.Join(dir, "leader-stopped"), nil, 0o600)
	os.Exit(0)
}
