package inbound

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
)

func TestEventDispatcherAcknowledgesReactionNotifications(t *testing.T) {
	// No store or sender is wired: acknowledging reactions needs neither.
	m := &Manager{}
	events := m.newEventDispatcher("cli_test")
	for _, kind := range []string{"im.message.reaction.created_v1", "im.message.reaction.deleted_v1"} {
		t.Run(kind, func(t *testing.T) {
			payload := []byte(fmt.Sprintf(`{"schema":"2.0","header":{"event_type":%q},"event":{"message_id":"om_test","reaction_type":{"emoji_type":"Typing"}}}`, kind))
			if _, err := events.Do(context.Background(), payload); err != nil {
				t.Fatalf("reaction notification failed: %v", err)
			}
		})
	}
}

func TestEventDispatcherPreservesUnsupportedEventErrors(t *testing.T) {
	events := (&Manager{}).newEventDispatcher("cli_test")
	_, err := events.Do(t.Context(), []byte(`{"schema":"2.0","header":{"event_type":"im.chat.updated_v1"},"event":{}}`))
	var missing *dispatcher.NotFoundEventHandlerErr
	if !errors.As(err, &missing) {
		t.Fatalf("unregistered event error = %v, want missing handler", err)
	}
	_, err = events.Do(t.Context(), []byte(`{"schema":"2.0","header":{"event_type":"im.message.reaction.created_v1"},"event":42}`))
	if err == nil {
		t.Fatal("malformed reaction payload was accepted")
	}
}

func TestEventDispatcherKeepsMessageAndCardHandlers(t *testing.T) {
	m := &Manager{logger: log.Bg()}
	events := m.newEventDispatcher("cli_test")
	// The existing message handler acknowledges and drops missing message IDs.
	if _, err := events.Do(t.Context(), []byte(`{"schema":"2.0","header":{"event_type":"im.message.receive_v1"},"event":{}}`)); err != nil {
		t.Fatalf("message handler not reached: %v", err)
	}
	response, err := events.Do(t.Context(), []byte(`{"schema":"2.0","header":{"event_type":"card.action.trigger"},"event":{"action":{"value":{"action":"credential_form_acknowledged"}}}}`))
	if err != nil {
		t.Fatalf("card handler not reached: %v", err)
	}
	ack, ok := response.(*callback.CardActionTriggerResponse)
	if !ok || ack.Toast == nil || ack.Toast.Content != "This card has ended" {
		t.Fatalf("card response = %#v, want existing acknowledgement", response)
	}
}
