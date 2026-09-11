package agentdaemon

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
)

func TestAuthoringDoesNotBlockRunStream(t *testing.T) {
	for _, ending := range []string{"done", "cancel", "disconnect"} {
		t.Run(ending, func(t *testing.T) {
			c, _, session, conn, _ := newWiredHarness(t, "dev-1", "conv-1", "pa-1")
			defer session.Close("test done")
			started, stopped := make(chan struct{}, 4), make(chan struct{}, 4)
			c.authoring = func(ctx context.Context, _ string, _ proto.AuthoringRequestPayload) (any, error) {
				started <- struct{}{}
				<-ctx.Done()
				stopped <- struct{}{}
				return nil, ctx.Err()
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			ch, err := c.StreamPrompt(ctx, basicInput())
			if err != nil {
				t.Fatal(err)
			}
			if !waitForWrite(t, conn, proto.TypePromptRequest, 2*time.Second) {
				t.Fatal("prompt not sent")
			}
			for i := range 4 {
				env, _ := proto.NewEnvelope(proto.TypeAuthoringRequest, "run-1", proto.AuthoringRequestPayload{RequestID: fmt.Sprint(i), Operation: proto.AuthoringContext})
				conn.Feed(env)
				select {
				case <-started:
				case <-time.After(2 * time.Second):
					t.Fatal("command was blocked behind another command")
				}
			}
			busy, _ := proto.NewEnvelope(proto.TypeAuthoringRequest, "run-1", proto.AuthoringRequestPayload{RequestID: "busy", Operation: proto.AuthoringContext})
			conn.Feed(busy)
			if !waitForWrite(t, conn, proto.TypeAuthoringResponse, 2*time.Second) {
				t.Fatal("overloaded command did not receive a response")
			}
			for _, env := range conn.Writes() {
				if env.Type == proto.TypeAuthoringResponse {
					var response proto.AuthoringResponsePayload
					_ = env.DecodePayload(&response)
					if response.RequestID != "busy" || response.Error == "" {
						t.Fatal("overloaded command was not rejected")
					}
				}
			}
			for range 64 {
				delta, _ := proto.NewEnvelope(proto.TypeDelta, "run-1", proto.DeltaPayload{Delta: "x"})
				conn.Feed(delta)
				select {
				case event := <-ch:
					if event.Type != connector.EventDelta || event.Delta != "x" {
						t.Fatalf("unexpected event: %+v", event)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("slow command blocked the run stream")
				}
			}
			switch ending {
			case "done":
				done, _ := proto.NewEnvelope(proto.TypeDone, "run-1", proto.DonePayload{Content: "done"})
				conn.Feed(done)
			case "cancel":
				cancel()
			case "disconnect":
				session.Close("connection lost")
			}
			_, final := drainEvents(ch, t)
			if final == nil {
				t.Fatal("run completion lost")
			}
			for range 4 {
				select {
				case <-stopped:
				case <-time.After(2 * time.Second):
					t.Fatal("command outlived run stream")
				}
			}
		})
	}
}
