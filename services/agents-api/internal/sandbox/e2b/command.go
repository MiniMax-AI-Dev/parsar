package e2b

import (
	"bytes"
	"context"
	"errors"
	"path"

	"connectrpc.com/connect"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	process "github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/e2b/envdprocess"
	"google.golang.org/protobuf/proto"
)

func (p *Provider) RunCommand(ctx context.Context, r sandbox.Reference, c sandbox.Command) (sandbox.CommandResult, error) {
	if len(c.Args) == 0 || c.Args[0] == "" || (c.Directory != "" && !path.IsAbs(c.Directory)) {
		return sandbox.CommandResult{}, sandbox.ErrInvalid
	}
	a, e := p.inspect(ctx, r)
	if e != nil {
		return sandbox.CommandResult{}, e
	}
	return p.run(ctx, a, "runtime", c)
}

// Only trusted initialization calls this transport. Native execution and daily
// Files stay on the authenticated Core/Runtime connection.
func (p *Provider) run(ctx context.Context, a allocation, user string, c sandbox.Command) (sandbox.CommandResult, error) {
	var result sandbox.CommandResult
	if _, ok := ctx.Deadline(); !ok || a.AccessToken == "" {
		return result, sandbox.ErrInvalid
	}
	request := connect.NewRequest(&process.StartRequest{Process: &process.ProcessConfig{Cmd: c.Args[0], Args: c.Args[1:], Cwd: nil}, Stdin: proto.Bool(false)})
	if c.Directory != "" {
		request.Msg.Process.Cwd = proto.String(c.Directory)
	}
	request.Header().Set("X-Access-Token", a.AccessToken)
	request.Header().Set("E2b-Sandbox-Id", a.ID)
	request.Header().Set("E2b-Sandbox-Port", "49983")
	request.Header().Set("Authorization", basicUser(user))
	client := connect.NewClient[process.StartRequest, process.StartResponse](p.client, envdURL+"/process.Process/Start", connect.WithReadMaxBytes(1024*1024))
	stream, e := client.CallServerStream(ctx, request)
	if e != nil {
		return result, errors.Join(sandbox.ErrCommandUnconfirmed, ctx.Err())
	}
	defer stream.Close()
	var stdout, stderr bytes.Buffer
	ended := false
	for stream.Receive() {
		event := stream.Msg().GetEvent()
		if data := event.GetData(); data != nil {
			if stdout.Len()+stderr.Len()+len(data.GetStdout())+len(data.GetStderr()) > 1024*1024 {
				return result, sandbox.ErrCommandUnconfirmed
			}
			stdout.Write(data.GetStdout())
			stderr.Write(data.GetStderr())
		}
		if end := event.GetEnd(); end != nil {
			if ended || !end.GetExited() {
				return result, sandbox.ErrCommandUnconfirmed
			}
			result.ExitCode = int(end.GetExitCode())
			ended = true
		}
	}
	if stream.Err() != nil || !ended {
		return sandbox.CommandResult{}, errors.Join(sandbox.ErrCommandUnconfirmed, ctx.Err())
	}
	result.Stdout, result.Stderr = stdout.String(), stderr.String()
	return result, nil
}
