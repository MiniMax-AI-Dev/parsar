package slack

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/slack-go/slack"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// fakeSender records the explicit arguments the adapter passes so tests assert
// channel/thread/text/blocks routing without slack-go's HTTP client.
type fakeSender struct {
	postChannel string
	postThread  string
	postText    string
	postBlocks  []slack.Block
	postTS      string
	postErr     error

	updChannel string
	updTS      string
	updText    string
	updBlocks  []slack.Block
	updErr     error
}

func (f *fakeSender) post(_ context.Context, channelID, threadTS, text string, blocks []slack.Block) (string, error) {
	f.postChannel, f.postThread, f.postText, f.postBlocks = channelID, threadTS, text, blocks
	return f.postTS, f.postErr
}

func (f *fakeSender) update(_ context.Context, channelID, ts, text string, blocks []slack.Block) error {
	f.updChannel, f.updTS, f.updText, f.updBlocks = channelID, ts, text, blocks
	return f.updErr
}

// channelWithFake builds an adapter whose transport is the supplied fake,
// injected through the WithSenderFactory seam.
func channelWithFake(fake *fakeSender) *Channel {
	return New(Config{AppID: "A123", BotToken: "xoxb-test"},
		WithSenderFactory(func(string) slackSender { return fake }))
}

// blockCard renders a one-line progress card so transport tests have a real
// Block Kit payload to round-trip through cardContent.
func blockCard(t *testing.T) channel.Card {
	t.Helper()
	c := newTestChannel()
	card, err := c.RenderProgress(context.Background(), channel.ReplyTarget{}, channel.ProgressState{Title: "Demo"})
	if err != nil {
		t.Fatalf("RenderProgress: %v", err)
	}
	return card
}

func TestReply_PostsPlainTextThreaded(t *testing.T) {
	fake := &fakeSender{postTS: "1700000000.000100"}
	c := channelWithFake(fake)

	target := channel.ReplyTarget{ExternalChatID: "C1", ExternalThreadID: "T9"}
	if err := c.Reply(context.Background(), target, "ack"); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if fake.postChannel != "C1" {
		t.Errorf("channel = %q, want C1", fake.postChannel)
	}
	if fake.postThread != "T9" {
		t.Errorf("thread = %q, want T9 (thread anchor)", fake.postThread)
	}
	if fake.postText != "ack" {
		t.Errorf("text = %q, want ack", fake.postText)
	}
	if fake.postBlocks != nil {
		t.Errorf("Reply must send no blocks, got %d", len(fake.postBlocks))
	}
}

func TestReply_FallsBackToReplyToMessageID(t *testing.T) {
	fake := &fakeSender{}
	c := channelWithFake(fake)

	// No ExternalThreadID -> anchor on the replied-to message ts.
	target := channel.ReplyTarget{ExternalChatID: "C1", ReplyToMessageID: "M5"}
	if err := c.Reply(context.Background(), target, "ack"); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if fake.postThread != "M5" {
		t.Errorf("thread = %q, want M5", fake.postThread)
	}
}

func TestSend_ReturnsTSAndCarriesBlocks(t *testing.T) {
	fake := &fakeSender{postTS: "1700000000.000200"}
	c := channelWithFake(fake)
	card := blockCard(t)

	ref, err := c.Send(context.Background(), channel.ReplyTarget{ExternalChatID: "C1"}, card)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if ref.ID != "1700000000.000200" {
		t.Errorf("ref.ID = %q, want the sender ts", ref.ID)
	}
	if ref.Text == "" {
		t.Error("ref.Text must carry the card fallback text")
	}
	if len(fake.postBlocks) == 0 {
		t.Error("Send must forward the decoded Block Kit blocks")
	}
	if fake.postChannel != "C1" {
		t.Errorf("channel = %q, want C1", fake.postChannel)
	}
}

func TestEdit_UpdatesByChannelAndTS(t *testing.T) {
	fake := &fakeSender{}
	c := channelWithFake(fake)
	card := blockCard(t)

	target := channel.ReplyTarget{ExternalChatID: "C1"}
	ref := gateway.MessageRef{ID: "1700000000.000200"}
	if err := c.Edit(context.Background(), target, ref, card); err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if fake.updChannel != "C1" {
		t.Errorf("update channel = %q, want C1", fake.updChannel)
	}
	if fake.updTS != "1700000000.000200" {
		t.Errorf("update ts = %q, want the ref ts", fake.updTS)
	}
	if len(fake.updBlocks) == 0 {
		t.Error("Edit must forward the decoded blocks")
	}
}

func TestEdit_RequiresTSAndChannel(t *testing.T) {
	c := channelWithFake(&fakeSender{})
	card := blockCard(t)

	// Missing ts.
	err := c.Edit(context.Background(), channel.ReplyTarget{ExternalChatID: "C1"}, gateway.MessageRef{}, card)
	if err == nil {
		t.Error("Edit without a message ts must error")
	}
	// Missing channel.
	err = c.Edit(context.Background(), channel.ReplyTarget{}, gateway.MessageRef{ID: "1.2"}, card)
	if err == nil {
		t.Error("Edit without a channel id must error")
	}
}

func TestCardContent_EmptyPayloadYieldsNoBlocks(t *testing.T) {
	text, blocks, err := cardContent(channel.Card{})
	if err != nil {
		t.Fatalf("cardContent: %v", err)
	}
	if text != "" || blocks != nil {
		t.Errorf("empty payload must yield no text/blocks, got %q / %d blocks", text, len(blocks))
	}
}

func TestCardContent_DecodesTextAndBlocks(t *testing.T) {
	card := blockCard(t)
	text, blocks, err := cardContent(card)
	if err != nil {
		t.Fatalf("cardContent: %v", err)
	}
	if text == "" {
		t.Error("want fallback text decoded from payload")
	}
	if len(blocks) == 0 {
		t.Error("want typed blocks decoded from payload")
	}
	// Sanity: the payload really is the JSON we rendered.
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(card.Payload, &probe); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if _, ok := probe["blocks"]; !ok {
		t.Error("payload missing blocks field")
	}
}

// --- Error-path coverage ---------------------------------------------------

// noTokenChannel resolves credentials with no bot token, so senderFor fails
// before any transport call — exercising the credential-failure branch shared
// by Reply/Send/Edit.
func noTokenChannel() *Channel {
	return New(Config{AppID: "A123"}) // no BotToken
}

func TestReply_PropagatesCredentialError(t *testing.T) {
	err := noTokenChannel().Reply(context.Background(), channel.ReplyTarget{ExternalChatID: "C1"}, "ack")
	if err != errNoBotToken {
		t.Fatalf("Reply err = %v, want errNoBotToken", err)
	}
}

func TestSend_PropagatesCredentialError(t *testing.T) {
	_, err := noTokenChannel().Send(context.Background(), channel.ReplyTarget{ExternalChatID: "C1"}, channel.Card{})
	if err != errNoBotToken {
		t.Fatalf("Send err = %v, want errNoBotToken", err)
	}
}

func TestEdit_PropagatesCredentialError(t *testing.T) {
	ref := gateway.MessageRef{ID: "1.2"}
	err := noTokenChannel().Edit(context.Background(), channel.ReplyTarget{ExternalChatID: "C1"}, ref, channel.Card{})
	if err != errNoBotToken {
		t.Fatalf("Edit err = %v, want errNoBotToken", err)
	}
}

func TestSend_PropagatesPostError(t *testing.T) {
	want := errors.New("slack down")
	fake := &fakeSender{postErr: want}
	_, err := channelWithFake(fake).Send(context.Background(), channel.ReplyTarget{ExternalChatID: "C1"}, blockCard(t))
	if err != want {
		t.Fatalf("Send err = %v, want %v", err, want)
	}
}

func TestReply_PropagatesPostError(t *testing.T) {
	want := errors.New("slack down")
	fake := &fakeSender{postErr: want}
	err := channelWithFake(fake).Reply(context.Background(), channel.ReplyTarget{ExternalChatID: "C1"}, "ack")
	if err != want {
		t.Fatalf("Reply err = %v, want %v", err, want)
	}
}

func TestEdit_PropagatesUpdateError(t *testing.T) {
	want := errors.New("update failed")
	fake := &fakeSender{updErr: want}
	ref := gateway.MessageRef{ID: "1.2"}
	err := channelWithFake(fake).Edit(context.Background(), channel.ReplyTarget{ExternalChatID: "C1"}, ref, blockCard(t))
	if err != want {
		t.Fatalf("Edit err = %v, want %v", err, want)
	}
}

// TestSend_RejectsMalformedCardPayload exercises cardContent's decode-error
// branch: a card whose payload is not the Block Kit JSON shape must surface a
// decode error rather than posting garbage.
func TestSend_RejectsMalformedCardPayload(t *testing.T) {
	fake := &fakeSender{}
	bad := channel.Card{Payload: []byte("not json")}
	_, err := channelWithFake(fake).Send(context.Background(), channel.ReplyTarget{ExternalChatID: "C1"}, bad)
	if err == nil {
		t.Fatal("Send must reject a malformed card payload")
	}
	if fake.postChannel != "" {
		t.Error("Send must not call the transport when decoding fails")
	}
}

func TestEdit_RejectsMalformedCardPayload(t *testing.T) {
	fake := &fakeSender{}
	bad := channel.Card{Payload: []byte("not json")}
	ref := gateway.MessageRef{ID: "1.2"}
	err := channelWithFake(fake).Edit(context.Background(), channel.ReplyTarget{ExternalChatID: "C1"}, ref, bad)
	if err == nil {
		t.Fatal("Edit must reject a malformed card payload")
	}
	if fake.updChannel != "" {
		t.Error("Edit must not call the transport when decoding fails")
	}
}
