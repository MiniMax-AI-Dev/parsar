package discord

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/bwmarrin/discordgo"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// fakeSender records the explicit arguments the adapter passes so tests assert
// channel/content/embeds/components routing without discordgo's HTTP client.
type fakeSender struct {
	postChannel    string
	postContent    string
	postEmbeds     []*discordgo.MessageEmbed
	postComponents []discordgo.MessageComponent
	postID         string
	postErr        error

	updChannel    string
	updID         string
	updContent    string
	updEmbeds     []*discordgo.MessageEmbed
	updComponents []discordgo.MessageComponent
	updErr        error
}

func (f *fakeSender) post(_ context.Context, channelID, content string, embeds []*discordgo.MessageEmbed, components []discordgo.MessageComponent) (string, error) {
	f.postChannel, f.postContent, f.postEmbeds, f.postComponents = channelID, content, embeds, components
	return f.postID, f.postErr
}

func (f *fakeSender) update(_ context.Context, channelID, msgID, content string, embeds []*discordgo.MessageEmbed, components []discordgo.MessageComponent) error {
	f.updChannel, f.updID, f.updContent, f.updEmbeds, f.updComponents = channelID, msgID, content, embeds, components
	return f.updErr
}

// channelWithFake builds an adapter whose transport is the supplied fake,
// injected through the WithSenderFactory seam.
func channelWithFake(fake *fakeSender) *Channel {
	return New(Config{AppID: "A123", BotToken: "bot-test"},
		WithSenderFactory(func(string) discordSender { return fake }))
}

// embedCard renders a one-line progress card so transport tests have a real
// embed payload to round-trip through cardContent.
func embedCard(t *testing.T) channel.Card {
	t.Helper()
	card, err := newTestChannel().RenderProgress(context.Background(), channel.ReplyTarget{}, channel.ProgressState{Title: "Demo"})
	if err != nil {
		t.Fatalf("RenderProgress: %v", err)
	}
	return card
}

// permissionCard renders an interactive card so component round-tripping is
// exercised (buttons must survive cardContent → discordgo translation).
func permissionCard(t *testing.T) channel.Card {
	t.Helper()
	card, err := newTestChannel().RenderPermission(context.Background(), channel.ReplyTarget{}, channel.PermissionRequest{
		Title: "Demo", ToolName: "Bash", ToolInput: "ls", RequestID: "req-1",
	})
	if err != nil {
		t.Fatalf("RenderPermission: %v", err)
	}
	return card
}

func TestReply_PostsPlainText(t *testing.T) {
	fake := &fakeSender{postID: "msg-1"}
	c := channelWithFake(fake)

	target := channel.ReplyTarget{ExternalChatID: "C1"}
	if err := c.Reply(context.Background(), target, "ack"); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if fake.postChannel != "C1" {
		t.Errorf("channel = %q, want C1", fake.postChannel)
	}
	if fake.postContent != "ack" {
		t.Errorf("content = %q, want ack", fake.postContent)
	}
	if fake.postEmbeds != nil || fake.postComponents != nil {
		t.Errorf("Reply must send no embeds/components, got %d/%d", len(fake.postEmbeds), len(fake.postComponents))
	}
}

func TestReply_AlwaysPostsToChatChannel(t *testing.T) {
	fake := &fakeSender{}
	c := channelWithFake(fake)

	// ExternalThreadID carries the conversation grouping key (ThreadKey, which
	// falls back to the originating message id) — NOT a postable Discord
	// channel. A Discord thread message already arrives with channel_id == the
	// thread id, so ExternalChatID is always the correct post target and the
	// thread slot must never override it (honouring it POSTed result cards to a
	// message-id-as-channel → "Unknown Channel" 10003).
	target := channel.ReplyTarget{ExternalChatID: "C1", ExternalThreadID: "TH9"}
	if err := c.Reply(context.Background(), target, "ack"); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if fake.postChannel != "C1" {
		t.Errorf("channel = %q, want C1 (chat channel, never the thread/grouping key)", fake.postChannel)
	}
}

func TestSend_ReturnsIDAndCarriesEmbeds(t *testing.T) {
	fake := &fakeSender{postID: "msg-200"}
	c := channelWithFake(fake)

	ref, err := c.Send(context.Background(), channel.ReplyTarget{ExternalChatID: "C1"}, embedCard(t))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if ref.ID != "msg-200" {
		t.Errorf("ref.ID = %q, want the sender message id", ref.ID)
	}
	if ref.Text == "" {
		t.Error("ref.Text must carry the card content fallback")
	}
	if len(fake.postEmbeds) == 0 {
		t.Error("Send must forward the decoded embed")
	}
	if fake.postChannel != "C1" {
		t.Errorf("channel = %q, want C1", fake.postChannel)
	}
}

func TestSend_TranslatesComponents(t *testing.T) {
	fake := &fakeSender{postID: "msg-1"}
	c := channelWithFake(fake)

	if _, err := c.Send(context.Background(), channel.ReplyTarget{ExternalChatID: "C1"}, permissionCard(t)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if len(fake.postComponents) != 1 {
		t.Fatalf("want 1 action row, got %d", len(fake.postComponents))
	}
	row, ok := fake.postComponents[0].(discordgo.ActionsRow)
	if !ok {
		t.Fatalf("component 0 is not an ActionsRow, got %T", fake.postComponents[0])
	}
	if len(row.Components) != 2 {
		t.Fatalf("want 2 buttons in the row, got %d", len(row.Components))
	}
	allow, ok := row.Components[0].(discordgo.Button)
	if !ok {
		t.Fatalf("row component 0 is not a Button, got %T", row.Components[0])
	}
	if allow.CustomID != "permission_allow:req-1" {
		t.Errorf("allow custom_id = %q, want permission_allow:req-1", allow.CustomID)
	}
	if allow.Style != discordgo.SuccessButton {
		t.Errorf("allow style = %d, want SuccessButton", allow.Style)
	}
}

func TestSend_TranslatesSelectMenu(t *testing.T) {
	fake := &fakeSender{postID: "msg-1"}
	c := channelWithFake(fake)

	card, err := newTestChannel().RenderChoiceForm(context.Background(), channel.ReplyTarget{}, channel.ChoiceForm{
		Title: "Pick", RequestID: "f1",
		Questions: []channel.ChoiceQuestion{{Header: "Env", Question: "which?", MultiSelect: true, Options: []string{"a", "b"}}},
	})
	if err != nil {
		t.Fatalf("RenderChoiceForm: %v", err)
	}
	if _, err := c.Send(context.Background(), channel.ReplyTarget{ExternalChatID: "C1"}, card); err != nil {
		t.Fatalf("Send: %v", err)
	}
	// First row is the select.
	row := fake.postComponents[0].(discordgo.ActionsRow)
	sel, ok := row.Components[0].(discordgo.SelectMenu)
	if !ok {
		t.Fatalf("row 0 component is not a SelectMenu, got %T", row.Components[0])
	}
	if sel.MenuType != discordgo.StringSelectMenu {
		t.Errorf("menu type = %d, want StringSelectMenu", sel.MenuType)
	}
	if sel.CustomID != "ask_user_choice_pick:0" {
		t.Errorf("select custom_id = %q", sel.CustomID)
	}
	if sel.MinValues == nil || *sel.MinValues != 0 {
		t.Errorf("min values = %v, want 0", sel.MinValues)
	}
	if sel.MaxValues != 2 { // multiselect → len(options)
		t.Errorf("max values = %d, want 2", sel.MaxValues)
	}
}

func TestEdit_UpdatesByChannelAndID(t *testing.T) {
	fake := &fakeSender{}
	c := channelWithFake(fake)

	target := channel.ReplyTarget{ExternalChatID: "C1"}
	ref := gateway.MessageRef{ID: "msg-200"}
	if err := c.Edit(context.Background(), target, ref, embedCard(t)); err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if fake.updChannel != "C1" {
		t.Errorf("update channel = %q, want C1", fake.updChannel)
	}
	if fake.updID != "msg-200" {
		t.Errorf("update id = %q, want the ref id", fake.updID)
	}
	if len(fake.updEmbeds) == 0 {
		t.Error("Edit must forward the decoded embed")
	}
}

func TestEdit_RequiresIDAndChannel(t *testing.T) {
	c := channelWithFake(&fakeSender{})
	card := embedCard(t)

	// Missing message id.
	if err := c.Edit(context.Background(), channel.ReplyTarget{ExternalChatID: "C1"}, gateway.MessageRef{}, card); err == nil {
		t.Error("Edit without a message id must error")
	}
	// Missing channel id.
	if err := c.Edit(context.Background(), channel.ReplyTarget{}, gateway.MessageRef{ID: "m1"}, card); err == nil {
		t.Error("Edit without a channel id must error")
	}
}

func TestCardContent_EmptyPayloadYieldsContentOnly(t *testing.T) {
	content, embeds, components, err := cardContent(channel.Card{})
	if err != nil {
		t.Fatalf("cardContent: %v", err)
	}
	if content != "" || embeds != nil || components != nil {
		t.Errorf("empty payload must yield nothing, got %q / %d / %d", content, len(embeds), len(components))
	}
}

func TestCardContent_DecodesContentAndEmbeds(t *testing.T) {
	content, embeds, _, err := cardContent(embedCard(t))
	if err != nil {
		t.Fatalf("cardContent: %v", err)
	}
	if content == "" {
		t.Error("want content fallback decoded from payload")
	}
	if len(embeds) == 0 {
		t.Error("want typed embeds decoded from payload")
	}
	// Sanity: the payload really is the JSON we rendered.
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(embedCard(t).Payload, &probe); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if _, ok := probe["embeds"]; !ok {
		t.Error("payload missing embeds field")
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
	ref := gateway.MessageRef{ID: "m1"}
	err := noTokenChannel().Edit(context.Background(), channel.ReplyTarget{ExternalChatID: "C1"}, ref, channel.Card{})
	if err != errNoBotToken {
		t.Fatalf("Edit err = %v, want errNoBotToken", err)
	}
}

func TestSend_PropagatesPostError(t *testing.T) {
	want := errors.New("discord down")
	fake := &fakeSender{postErr: want}
	_, err := channelWithFake(fake).Send(context.Background(), channel.ReplyTarget{ExternalChatID: "C1"}, embedCard(t))
	if err != want {
		t.Fatalf("Send err = %v, want %v", err, want)
	}
}

func TestEdit_PropagatesUpdateError(t *testing.T) {
	want := errors.New("update failed")
	fake := &fakeSender{updErr: want}
	ref := gateway.MessageRef{ID: "m1"}
	err := channelWithFake(fake).Edit(context.Background(), channel.ReplyTarget{ExternalChatID: "C1"}, ref, embedCard(t))
	if err != want {
		t.Fatalf("Edit err = %v, want %v", err, want)
	}
}

// TestSend_RejectsMalformedCardPayload exercises cardContent's decode-error
// branch: a card whose payload is not the deMessage JSON shape must surface a
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

func TestDefaultSenderFactory_BuildsClientSender(t *testing.T) {
	// The production factory must build a non-nil discordgo-backed sender for a
	// static token (discordgo.New never errors on a bare bot token).
	s := defaultSenderFactory("bot-xyz")
	cs, ok := s.(clientSender)
	if !ok {
		t.Fatalf("defaultSenderFactory returned %T, want clientSender", s)
	}
	if cs.api == nil {
		t.Error("clientSender.api must be a built discordgo session")
	}
}
