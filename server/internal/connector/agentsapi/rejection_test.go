package agentsapi

import (
	"context"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/openai/openai-go/v3"
)

func TestTerminalAdmissionRejectionSettles(t *testing.T) {
	for _, code := range []string{"environment_unavailable", "environment_input_expired", "environment_input_cancelled"} {
		for _, status := range []string{"running", "cancelled"} {
			t.Run(code+"/"+status, func(t *testing.T) {
				st := &memoryStore{status: status}
				// Cancellation after an uncertain attempt must still resolve the admission.
				st.run.Attempted = status == "cancelled"
				api := &protocolServer{rejectionCode: code}
				c := newTestConnector(t, st, api)
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				events := drain(t, ctx, c, st)
				if ctx.Err() != nil || api.submissions != 1 || api.turns != 0 || !st.run.Settled || st.run.Submitted {
					t.Fatalf("rejection not settled: ctx=%v requests=%d run=%+v", ctx.Err(), api.submissions, st.run)
				}
				if status == "running" && (len(events) != 2 || events[0].Type != connector.EventError || events[1].Final.Metadata["error"] == nil) {
					t.Fatalf("missing explicit failure: %+v", events)
				}
				if status == "cancelled" && len(events) != 0 {
					t.Fatalf("cancelled run emitted new failure: %+v", events)
				}
			})
		}
	}
}

func TestUncertainAdmissionRetainsRetry(t *testing.T) {
	for _, err := range []*openai.Error{
		{StatusCode: 409, Code: "turn_conflict"}, {StatusCode: 409, Code: "idempotency_conflict"},
		{StatusCode: 502}, {StatusCode: 408}, {StatusCode: 429},
	} {
		if inputRejected(err) || !retryable(err) {
			t.Fatalf("uncertain receipt treated as rejection: %d %s", err.StatusCode, err.Code)
		}
	}
}
