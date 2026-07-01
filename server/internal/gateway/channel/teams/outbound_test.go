package teams

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// fakeSender is an in-memory teamsSender: it records the last send/edit so the
// outbound tests assert the Connector call shape without a live HTTP round-trip.
type fakeSender struct {
	sentServiceURL string
	sentConvID     string
	sentActivity   outboundActivity
	returnID       string

	editedActivityID string
	editedActivity   outboundActivity
}

func (f *fakeSender) send(_ context.Context, serviceURL, conversationID string, act outboundActivity) (string, error) {
	f.sentServiceURL = serviceURL
	f.sentConvID = conversationID
	f.sentActivity = act
	if f.returnID == "" {
		return "posted-1", nil
	}
	return f.returnID, nil
}

func (f *fakeSender) edit(_ context.Context, _, _, activityID string, act outboundActivity) error {
	f.editedActivityID = activityID
	f.editedActivity = act
	return nil
}

// channelWithFakeSender wires a fake sender and pre-primes a conversation ref so
// serviceURLFor resolves without an inbound.
func channelWithFakeSender(convID, serviceURL string) (*Channel, *fakeSender) {
	fs := &fakeSender{}
	store := NewMemoryConversationStore()
	store.Put(convID, ConversationRef{ServiceURL: serviceURL})
	c := New(
		Config{AppID: "app-123", AppPassword: "secret"},
		WithConversationStore(store),
		WithSenderFactory(func(channel.Credential) teamsSender { return fs }),
	)
	return c, fs
}

func TestActivitiesURL(t *testing.T) {
	got, err := activitiesURL("https://smba.trafficmanager.net/amer/", "conv-9")
	if err != nil {
		t.Fatalf("activitiesURL: %v", err)
	}
	want := "https://smba.trafficmanager.net/amer/v3/conversations/conv-9/activities"
	if got != want {
		t.Errorf("activitiesURL = %q, want %q", got, want)
	}
	if _, err := activitiesURL("", "conv-9"); err == nil {
		t.Error("empty serviceUrl (ref not primed) must error")
	}
	if _, err := activitiesURL("https://x", ""); err == nil {
		t.Error("empty conversation id must error")
	}
}

func TestBuildActivity(t *testing.T) {
	plain := buildActivity("hello", nil, "reply-1")
	if plain.Type != "message" || plain.Text != "hello" || plain.ReplyToID != "reply-1" {
		t.Errorf("plain activity = %+v", plain)
	}
	if len(plain.Attachments) != 0 {
		t.Error("text-only activity must carry no attachment")
	}
	carded := buildActivity("fallback", json.RawMessage(`{"type":"AdaptiveCard"}`), "")
	if len(carded.Attachments) != 1 {
		t.Fatalf("card activity must carry one attachment, got %d", len(carded.Attachments))
	}
	if carded.Attachments[0].ContentType != adaptiveCardContentType {
		t.Errorf("attachment contentType = %q", carded.Attachments[0].ContentType)
	}
}

func TestReply_PostsThroughSender(t *testing.T) {
	c, fs := channelWithFakeSender("conv-9", "https://smba.trafficmanager.net/amer/")
	err := c.Reply(context.Background(), channel.ReplyTarget{
		ExternalChatID:   "conv-9",
		ReplyToMessageID: "root-1",
	}, "ack text")
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if fs.sentConvID != "conv-9" {
		t.Errorf("sent to conv %q, want conv-9", fs.sentConvID)
	}
	if fs.sentServiceURL != "https://smba.trafficmanager.net/amer/" {
		t.Errorf("serviceURL = %q, want the primed ref", fs.sentServiceURL)
	}
	if fs.sentActivity.Text != "ack text" || fs.sentActivity.ReplyToID != "root-1" {
		t.Errorf("activity = %+v", fs.sentActivity)
	}
}

func TestSend_ReturnsActivityID(t *testing.T) {
	c, fs := channelWithFakeSender("conv-9", "https://smba.trafficmanager.net/amer/")
	fs.returnID = "posted-42"
	payload, _ := json.Marshal(teamsWireMessage{Text: "hi", Card: json.RawMessage(`{"type":"AdaptiveCard"}`)})
	ref, err := c.Send(context.Background(), channel.ReplyTarget{ExternalChatID: "conv-9"}, channel.Card{Payload: payload})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if ref.ID != "posted-42" {
		t.Errorf("ref.ID = %q, want posted-42", ref.ID)
	}
	if len(fs.sentActivity.Attachments) != 1 {
		t.Error("Send must attach the card")
	}
}

func TestEdit_RequiresActivityID(t *testing.T) {
	c, _ := channelWithFakeSender("conv-9", "https://smba.trafficmanager.net/amer/")
	err := c.Edit(context.Background(), channel.ReplyTarget{ExternalChatID: "conv-9"}, gateway.MessageRef{}, channel.Card{})
	if err == nil {
		t.Fatal("Edit with no activity id must error")
	}
}

func TestTokenAuthority(t *testing.T) {
	multi := &connectorSender{}
	if got := multi.tokenAuthority(); got != "https://login.microsoftonline.com/botframework.com/oauth2/v2.0/token" {
		t.Errorf("multi-tenant authority = %q", got)
	}
	single := &connectorSender{tenantID: "tenant-7"}
	if got := single.tokenAuthority(); got != "https://login.microsoftonline.com/tenant-7/oauth2/v2.0/token" {
		t.Errorf("single-tenant authority = %q", got)
	}
}
