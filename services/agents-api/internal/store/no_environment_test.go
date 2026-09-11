package store_test

import (
	"context"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestNoEnvironmentRejectsUnadvertisedDeviceBeforeClaim(t *testing.T) {
	h := newDispatchHarness(t)
	ctx := context.Background()
	var err error
	h.session, err = h.s.CreateSession(ctx, h.tenant, store.CreateSessionInput{Engine: "codex", IdempotencyKey: "none", Configuration: []byte(`{"agent":{"model":"test-model"},"environment":{"type":"none"}}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err = h.s.BindSessionDevice(ctx, h.tenant, h.session.ID, h.device.ID); err != nil {
		t.Fatal(err)
	}
	input := h.message("first", "Answer")
	if _, err = h.d.Run(ctx, h.tenant, h.session.ID, input.TurnID); err == nil {
		t.Fatal("unsupported environment admitted")
	}
	turn, err := h.s.GetTurn(ctx, h.tenant, h.session.ID, input.TurnID)
	if err != nil || turn.Status != store.TurnQueued {
		t.Fatal(turn, err)
	}
}
