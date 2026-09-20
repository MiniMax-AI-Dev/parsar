package e2b

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox"
	process "github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/sandbox/e2b/envdprocess"
)

type inputFixtureTransport struct{ target *url.URL }

func (t inputFixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	copy := r.Clone(r.Context())
	copy.URL.Scheme = t.target.Scheme
	copy.URL.Host = t.target.Host
	return http.DefaultTransport.RoundTrip(copy)
}

func TestStdinExitRequiresCompleteDeliveryAndTerminalStream(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		sendFailure, closeFailure connect.Code
		terminal, want            bool
	}{
		{name: "EOF", terminal: true, want: true},
		{name: "exit before EOF", closeFailure: connect.CodeNotFound, terminal: true, want: true},
		{name: "missing exit", closeFailure: connect.CodeNotFound},
		{name: "input not delivered", sendFailure: connect.CodeNotFound, terminal: true},
		{name: "unconfirmed EOF", closeFailure: connect.CodeUnavailable, terminal: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			finished := make(chan struct{})
			var once sync.Once
			finish := func() { once.Do(func() { close(finished) }) }
			mux := http.NewServeMux()
			mux.Handle("/process.Process/Start", connect.NewServerStreamHandler("/process.Process/Start", func(ctx context.Context, _ *connect.Request[process.StartRequest], s *connect.ServerStream[process.StartResponse]) error {
				if err := s.Send(&process.StartResponse{Event: &process.ProcessEvent{Event: &process.ProcessEvent_Start{Start: &process.ProcessEvent_StartEvent{Pid: 123}}}}); err != nil {
					return err
				}
				select {
				case <-finished:
				case <-ctx.Done():
					return ctx.Err()
				}
				if !tc.terminal {
					return nil
				}
				return s.Send(&process.StartResponse{Event: &process.ProcessEvent{Event: &process.ProcessEvent_End{End: &process.ProcessEvent_EndEvent{Exited: true}}}})
			}))
			mux.Handle("/process.Process/SendInput", connect.NewUnaryHandler("/process.Process/SendInput", func(context.Context, *connect.Request[process.SendInputRequest]) (*connect.Response[process.SendInputResponse], error) {
				if tc.sendFailure != 0 {
					finish()
					return nil, connect.NewError(tc.sendFailure, errors.New("fixture input failure"))
				}
				return connect.NewResponse(&process.SendInputResponse{}), nil
			}))
			mux.Handle("/process.Process/CloseStdin", connect.NewUnaryHandler("/process.Process/CloseStdin", func(context.Context, *connect.Request[process.CloseStdinRequest]) (*connect.Response[process.CloseStdinResponse], error) {
				finish()
				if tc.closeFailure != 0 {
					return nil, connect.NewError(tc.closeFailure, errors.New("fixture EOF failure"))
				}
				return connect.NewResponse(&process.CloseStdinResponse{}), nil
			}))
			server := httptest.NewServer(mux)
			defer server.Close()
			target, _ := url.Parse(server.URL)
			provider := Provider{client: &http.Client{Transport: inputFixtureTransport{target: target}}}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			_, err := provider.run(ctx, allocation{ID: "fixture", AccessToken: "fixture"}, "runtime", sandbox.Command{Args: []string{"fixture"}, Stdin: []byte("file")})
			if (err == nil) != tc.want {
				t.Fatal("unexpected stdin completion", err)
			}
		})
	}
}
