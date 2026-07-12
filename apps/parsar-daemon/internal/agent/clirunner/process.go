package clirunner

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

type StartOptions struct {
	Parent      context.Context
	Binary      string
	Args        []string
	Dir         string
	Env         []string
	NeedStdin   bool
	KillTimeout time.Duration
}

type Process struct {
	Cmd    *exec.Cmd
	Stdin  io.WriteCloser
	Stdout io.ReadCloser
	Stderr io.ReadCloser

	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	killAfter time.Duration

	cancelOnce sync.Once
	waitOnce   sync.Once
}

func Start(opts StartOptions) (*Process, error) {
	if opts.Parent == nil {
		opts.Parent = context.Background()
	}
	if opts.Binary == "" {
		return nil, fmt.Errorf("clirunner: binary required")
	}
	if opts.KillTimeout <= 0 {
		opts.KillTimeout = 3 * time.Second
	}

	ctx, cancel := context.WithCancel(opts.Parent)
	cmd := exec.CommandContext(ctx, opts.Binary, opts.Args...)
	cmd.Dir = opts.Dir
	if len(opts.Env) > 0 {
		cmd.Env = append([]string{}, opts.Env...)
	}

	var stdin io.WriteCloser
	var err error
	if opts.NeedStdin {
		stdin, err = cmd.StdinPipe()
		if err != nil {
			cancel()
			return nil, fmt.Errorf("clirunner: stdin pipe: %w", err)
		}
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		closePipe(stdin)
		cancel()
		return nil, fmt.Errorf("clirunner: stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		closePipe(stdin)
		cancel()
		return nil, fmt.Errorf("clirunner: stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		closePipe(stdin)
		cancel()
		return nil, fmt.Errorf("clirunner: start %q: %w", opts.Binary, err)
	}

	return &Process{
		Cmd:       cmd,
		Stdin:     stdin,
		Stdout:    stdout,
		Stderr:    stderr,
		ctx:       ctx,
		cancel:    cancel,
		done:      make(chan struct{}),
		killAfter: opts.KillTimeout,
	}, nil
}

func (p *Process) Context() context.Context {
	if p == nil || p.ctx == nil {
		return context.Background()
	}
	return p.ctx
}

func (p *Process) Done() <-chan struct{} {
	if p == nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return p.done
}

func (p *Process) Cancel() {
	if p == nil {
		return
	}
	p.cancelOnce.Do(func() {
		if p.Cmd != nil && p.Cmd.Process != nil {
			_ = p.Cmd.Process.Signal(syscall.SIGTERM)
			go func() {
				select {
				case <-p.done:
				case <-time.After(p.killAfter):
					_ = p.Cmd.Process.Signal(syscall.SIGKILL)
				}
			}()
		}
		if p.cancel != nil {
			p.cancel()
		}
	})
}

func (p *Process) Wait() error {
	if p == nil || p.Cmd == nil {
		return nil
	}
	err := p.Cmd.Wait()
	p.waitOnce.Do(func() { close(p.done) })
	return err
}

func closePipe(p io.Closer) {
	if p != nil {
		_ = p.Close()
	}
}
