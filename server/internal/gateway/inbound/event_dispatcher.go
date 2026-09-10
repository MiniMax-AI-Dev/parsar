package inbound

import (
	"context"

	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func (m *Manager) newEventDispatcher(appID string) *dispatcher.EventDispatcher {
	events := dispatcher.NewEventDispatcher("", "")
	events.OnP2MessageReceiveV1(func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
		return m.handleMessage(ctx, appID, event)
	})
	events.OnP2CardActionTrigger(func(ctx context.Context, event *callback.CardActionTriggerEvent) (*callback.CardActionTriggerResponse, error) {
		return m.handleCardAction(ctx, appID, event), nil
	})
	// Reaction notifications include our typing indicator. They do not start
	// runs or update reaction bookkeeping, which belongs to the send/undo path.
	events.OnP2MessageReactionCreatedV1(func(context.Context, *larkim.P2MessageReactionCreatedV1) error {
		return nil
	})
	events.OnP2MessageReactionDeletedV1(func(context.Context, *larkim.P2MessageReactionDeletedV1) error {
		return nil
	})
	return events
}
