package docker

import (
	"bytes"
	"context"
	"errors"
	"path"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"
)

// RunCommand is for trusted initialization only. Closing an exec attachment does
// not kill its process. On any uncertain outcome, the caller must reclaim the
// allocation with Kill instead of starting the daemon or replaying the command.
func (p *Provider) RunCommand(ctx context.Context, r sandbox.Reference, command sandbox.Command) (sandbox.CommandResult, error) {
	var result sandbox.CommandResult
	if len(command.Args) == 0 || (command.Directory != "" && !path.IsAbs(command.Directory)) {
		return result, sandbox.ErrInvalid
	}
	c, e := p.inspect(ctx, r)
	if e != nil {
		return result, e
	}
	if _, ok := ctx.Deadline(); !ok {
		return result, sandbox.ErrInvalid
	}
	exec, e := p.client.ExecCreate(ctx, c.Container.ID, client.ExecCreateOptions{User: "1000:1000", Cmd: command.Args, WorkingDir: command.Directory, AttachStdout: true, AttachStderr: true})
	if e != nil {
		return result, e
	}
	attached, e := p.client.ExecAttach(ctx, exec.ID, client.ExecAttachOptions{})
	if e != nil {
		return result, errors.Join(sandbox.ErrCommandUnconfirmed, e)
	}
	defer attached.Close()
	stdout, stderr := &boundedBuffer{}, &boundedBuffer{}
	done := make(chan error, 1)
	go func() { _, e := stdcopy.StdCopy(stdout, stderr, attached.Reader); done <- e }()
	select {
	case e = <-done:
	case <-ctx.Done():
		attached.Close()
		<-done
		e = ctx.Err()
	}
	if e != nil {
		return result, errors.Join(sandbox.ErrCommandUnconfirmed, e)
	}
	status, e := p.client.ExecInspect(ctx, exec.ID, client.ExecInspectOptions{})
	if e != nil || status.Running {
		return result, errors.Join(sandbox.ErrCommandUnconfirmed, e)
	}
	return sandbox.CommandResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: status.ExitCode}, nil
}

type boundedBuffer struct{ bytes.Buffer }

func (b *boundedBuffer) Write(data []byte) (int, error) {
	const max = 1024 * 1024
	if b.Len()+len(data) > max {
		return 0, errors.New("initialization output exceeds 1 MiB")
	}
	return b.Buffer.Write(data)
}
