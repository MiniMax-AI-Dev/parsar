package store

import (
	"sync"
	"testing"
)

func TestEnvironmentInputConcurrentPromotionClaimsOnce(t *testing.T) {
	s, pool := testStore(t)
	writer := executionLease(t, s).Store()
	tenant, session := environmentInputSession(t, s)
	pending := reserveEnvironmentInput(t, s, tenant, session.ID, "pending")
	const count = 8
	results := make(chan EnvironmentInputReservation, count)
	var group sync.WaitGroup
	for range count {
		group.Go(func() {
			got, err := writer.PromoteEnvironmentInput(t.Context(), tenant, session.ID, pending.ID)
			if err != nil {
				t.Error(err)
				return
			}
			results <- got
		})
	}
	group.Wait()
	close(results)
	var turnID string
	fresh, received := 0, 0
	for got := range results {
		received++
		if got.State != EnvironmentInputAdmitted || len(got.Receipts) != 2 {
			t.Fatal("promotion lost the original batch", got)
		}
		if turnID == "" {
			turnID = got.Receipts[0].TurnID
		}
		if !got.Receipts[0].Replayed {
			fresh++
		}
		for _, receipt := range got.Receipts {
			if receipt.TurnID != turnID || receipt.Replayed != got.Receipts[0].Replayed {
				t.Fatal("promotion changed execution ownership", got.Receipts)
			}
		}
	}
	if received != count || fresh != 1 {
		t.Fatal("promotion authorized multiple starts", received, fresh)
	}
	turn, err := s.GetTurn(t.Context(), tenant, session.ID, turnID)
	if err != nil || turn.Status != TurnInProgress || turn.StartedAt.IsZero() {
		t.Fatal("promotion did not persist its execution claim", turn, err)
	}
	environmentInputHistory(t, pool, session.ID, 1, 2)
	changes, err := s.ListSessionEvents(t.Context(), tenant, session.ID, 0)
	if err != nil || len(changes) < 2 {
		t.Fatal("missing promotion events", changes, err)
	}
	created, claimed := changes[0], changes[len(changes)-1]
	if created.Event.Type != "agent.session.turn.created" || created.Turn == nil || created.Turn.Status != TurnQueued || claimed.Event.Type != "agent.session.turn.in_progress" || claimed.Turn == nil || claimed.Turn.ID != turnID || claimed.Turn.Status != TurnInProgress {
		t.Fatal("claim reordered or replaced admission snapshots", created, claimed)
	}
	claimEvents := 0
	for _, change := range changes {
		if change.Event.Type == "agent.session.turn.in_progress" {
			claimEvents++
		}
	}
	if claimEvents != 1 {
		t.Fatal("retry published another claim", claimEvents)
	}
	transition(t, writer, tenant, session.ID, turnID, TurnInProgress, TurnCompleted)
	later := reserveEnvironmentInput(t, s, tenant, session.ID, "later")
	cursor, err := s.SessionEventCursor(t.Context(), tenant, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := writer.PromoteEnvironmentInput(t.Context(), tenant, session.ID, pending.ID)
	if err != nil || len(retry.Receipts) != 2 || !retry.Receipts[0].Replayed || retry.Receipts[0].TurnID != turnID {
		t.Fatal("terminal retry reclaimed execution", retry, err)
	}
	after, err := s.SessionEventCursor(t.Context(), tenant, session.ID)
	if err != nil || after != cursor {
		t.Fatal("terminal retry published events", after, cursor, err)
	}
	retained, err := s.GetEnvironmentInputReservation(t.Context(), tenant, session.ID, later.ID)
	if err != nil || retained.State != EnvironmentInputPending || !retained.Deadline.Equal(later.Deadline) {
		t.Fatal("old promotion affected new preparation", retained, err)
	}
}
