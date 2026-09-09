package dev

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/runstream"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/go-chi/chi/v5"
)

type firstEventWaitStore struct {
	stubRuntimeStore
	status string
	reads  int
	onWait func()
}

func (s *firstEventWaitStore) GetAgentRun(ctx context.Context, runID string) (store.AgentRunDetailRead, error) {
	s.reads++
	// The initial read and two observation intervals precede the outcome.
	if s.reads == 4 {
		s.onWait()
	}
	run, err := s.stubRuntimeStore.GetAgentRun(ctx, runID)
	run.Status = s.status
	return run, err
}

func TestConversationStreamWaitsForActiveRunOutcome(t *testing.T) {
	original := streamFirstEventTimeout
	streamFirstEventTimeout = 5 * time.Millisecond
	t.Cleanup(func() { streamFirstEventTimeout = original })

	for _, active := range []string{"queued", "running"} {
		for _, outcome := range []string{"event", "failed", "completed", "cancelled", "disconnect"} {
			t.Run(active+"/"+outcome, func(t *testing.T) {
				broker := runstream.NewBroker(runstream.DefaultBufferSize)
				s := &firstEventWaitStore{status: active}
				path := "/api/v1/conversations/" + testConversationID + "/runs/" + testRunID + "/stream"
				req := newConversationMessageRequest(http.MethodGet, path, "", store.DefaultDevFixtureIDs().UserID)
				ctx, cancel := context.WithTimeout(req.Context(), time.Second)
				defer cancel()
				s.onWait = func() {
					switch outcome {
					case "event":
						broker.Publish(testRunID, connector.PromptEvent{Type: connector.EventDone, Final: &connector.PromptOutput{Content: "delayed reply"}})
						broker.Finish(testRunID)
					case "disconnect":
						cancel()
					default:
						s.status = outcome
					}
				}
				r := chi.NewRouter()
				r.Get("/api/v1/conversations/{conversationID}/runs/{runID}/stream", streamConversationAgentRun(s, &routerConfig{runBroker: broker}))
				response := serveConversationMessageRequest(r, req.WithContext(ctx))
				if response.Code != http.StatusOK || s.reads < 4 {
					t.Fatalf("stream ended before the outcome: status=%d reads=%d body=%s", response.Code, s.reads, response.Body.String())
				}
				events := parseSSEFrames(t, response.Body)
				if outcome == "disconnect" {
					if len(events) != 0 || s.status != active {
						t.Fatalf("disconnect changed the run or emitted an error: status=%s events=%v", s.status, events)
					}
					return
				}
				if len(events) != 1 {
					t.Fatalf("events=%v, want one outcome event", events)
				}
				if outcome == "event" {
					if events[0]["type"] != "done" || s.status != active {
						t.Fatalf("delayed reply failed or read route changed run status: status=%s events=%v", s.status, events)
					}
				} else if reason, _ := events[0]["error"].(string); events[0]["type"] != "error" || !strings.Contains(reason, outcome) {
					t.Fatalf("terminal %s signal lost: %v", outcome, events)
				}
			})
		}
	}
}
