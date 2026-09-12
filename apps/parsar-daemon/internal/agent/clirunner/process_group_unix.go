//go:build unix

package clirunner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

func startProcessGroup(opts StartOptions) (*Process, error) {
	ctx, cancel := context.WithCancel(opts.Parent)
	cmd := exec.CommandContext(ctx, opts.Binary, opts.Args...)
	cmd.Dir = opts.Dir
	if len(opts.Env) > 0 {
		cmd.Env = append([]string{}, opts.Env...)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	group := &ownedGroup{cmd: cmd, killAfter: opts.KillTimeout}
	cmd.Cancel = group.cancel
	p := &Process{Cmd: cmd, ctx: ctx, cancel: cancel, done: make(chan struct{}), cancelProcess: group.cancel}

	// Cmd.Wait must reap the leader without closing output that consumers still need to drain.
	stdout, stdoutWriter, err := os.Pipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("clirunner: stdout pipe: %w", err)
	}
	stderr, stderrWriter, err := os.Pipe()
	if err != nil {
		cancel()
		closePipe(stdout)
		closePipe(stdoutWriter)
		return nil, fmt.Errorf("clirunner: stderr pipe: %w", err)
	}
	p.Stdout, p.Stderr = stdout, stderr
	cmd.Stdout, cmd.Stderr = stdoutWriter, stderrWriter
	if opts.NeedStdin {
		p.Stdin, err = cmd.StdinPipe()
	}
	if err == nil {
		err = cmd.Start()
	}
	closePipe(stdoutWriter)
	closePipe(stderrWriter)
	if err != nil {
		cancel()
		closePipe(p.Stdin)
		closePipe(stdout)
		closePipe(stderr)
		return nil, fmt.Errorf("clirunner: start %q: %w", opts.Binary, err)
	}

	var waitErr error
	p.waitProcess = func() error {
		<-p.done
		closePipe(stdout)
		closePipe(stderr)
		return waitErr
	}
	go func() {
		waitErr = cmd.Wait()
		group.finish()
		close(p.done)
	}()
	return p, nil
}

type ownedGroup struct {
	cmd       *exec.Cmd
	killAfter time.Duration
	mu        sync.Mutex
	timer     *time.Timer
	graceDone chan struct{}
	cancelled bool
	finished  bool
}

func (g *ownedGroup) cancel() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.finished {
		return os.ErrProcessDone
	}
	if g.cancelled {
		return nil
	}
	g.cancelled = true
	err := syscall.Kill(-g.cmd.Process.Pid, syscall.SIGTERM)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	g.graceDone = make(chan struct{})
	g.timer = time.AfterFunc(g.killAfter, func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		if !g.finished {
			_ = syscall.Kill(-g.cmd.Process.Pid, syscall.SIGKILL)
		}
		close(g.graceDone)
	})
	return err
}

func (g *ownedGroup) finish() {
	g.mu.Lock()
	if g.graceDone != nil && !errors.Is(syscall.Kill(-g.cmd.Process.Pid, 0), syscall.ESRCH) {
		// Descendants retain their TERM grace even when the leader has already exited.
		graceDone := g.graceDone
		g.mu.Unlock()
		<-graceDone
		g.mu.Lock()
	}
	defer g.mu.Unlock()
	g.finished = true
	if g.timer != nil {
		g.timer.Stop()
	}
	// A leader can exit while a descendant still holds the output pipes open.
	_ = syscall.Kill(-g.cmd.Process.Pid, syscall.SIGKILL)
}
