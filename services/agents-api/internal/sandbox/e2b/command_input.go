package e2b

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	process "github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/e2b/envdprocess"
)

func (p *Provider) sendInput(ctx context.Context, headers http.Header, pid uint32, input []byte) error {
	selector := &process.ProcessSelector{Selector: &process.ProcessSelector_Pid{Pid: pid}}
	sender := connect.NewClient[process.SendInputRequest, process.SendInputResponse](p.client, envdURL+"/process.Process/SendInput", connect.WithReadMaxBytes(65536))
	for len(input) > 0 {
		count := min(len(input), 1024*1024)
		request := connect.NewRequest(&process.SendInputRequest{Process: selector, Input: &process.ProcessInput{Input: &process.ProcessInput_Stdin{Stdin: input[:count]}}})
		copyCommandHeaders(request.Header(), headers)
		if _, err := sender.CallUnary(ctx, request); err != nil {
			return err
		}
		input = input[count:]
	}
	closer := connect.NewClient[process.CloseStdinRequest, process.CloseStdinResponse](p.client, envdURL+"/process.Process/CloseStdin", connect.WithReadMaxBytes(65536))
	request := connect.NewRequest(&process.CloseStdinRequest{Process: selector})
	copyCommandHeaders(request.Header(), headers)
	_, err := closer.CallUnary(ctx, request)
	// The process can exit after consuming all bytes, before this EOF request.
	// RunCommand still requires a complete successful exit stream.
	if connect.CodeOf(err) == connect.CodeNotFound {
		return nil
	}
	return err
}

func copyCommandHeaders(destination, source http.Header) {
	for _, name := range []string{"X-Access-Token", "E2b-Sandbox-Id", "E2b-Sandbox-Port", "Authorization"} {
		destination.Set(name, source.Get(name))
	}
}
