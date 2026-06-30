package discord

import (
	"testing"
)

// guildMessageJSON is a minimal Discord MESSAGE_CREATE payload (the wire shape
// discordgo marshals its *Message to) with a leading bot mention, two mentioned
// users, and a guild — the common @-mention trigger.
const guildMessageJSON = `{
	"id": "msg-100",
	"channel_id": "chan-1",
	"guild_id": "guild-9",
	"content": "<@bot-1> hello <@u-2>",
	"author": {"id": "u-1", "bot": false},
	"mentions": [{"id": "bot-1"}, {"id": "u-2"}],
	"type": 0
}`

func TestNormalize_GuildMessageMapsNeutralFields(t *testing.T) {
	ev, err := newTestChannel().Normalize([]byte(guildMessageJSON))
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if ev.Platform != "discord" {
		t.Errorf("Platform = %q, want discord", ev.Platform)
	}
	if ev.BotID != "A123" {
		t.Errorf("BotID = %q, want the app id A123", ev.BotID)
	}
	if ev.ExternalMessageID != "msg-100" {
		t.Errorf("ExternalMessageID = %q, want msg-100", ev.ExternalMessageID)
	}
	if ev.ExternalChatID != "chan-1" {
		t.Errorf("ExternalChatID = %q, want chan-1", ev.ExternalChatID)
	}
	if ev.ChatType != "channel" {
		t.Errorf("ChatType = %q, want channel", ev.ChatType)
	}
	if ev.Sender.TenantKey != "guild-9" {
		t.Errorf("Sender.TenantKey = %q, want guild-9 (guild_id)", ev.Sender.TenantKey)
	}
	if ev.Sender.PlatformUserID != "u-1" || ev.Sender.LocalUserID != "u-1" {
		t.Errorf("Sender ids = %q/%q, want u-1", ev.Sender.PlatformUserID, ev.Sender.LocalUserID)
	}
	// Leading bot mention is stripped; the mid-text mention stays in the prompt.
	if ev.Text != "hello <@u-2>" {
		t.Errorf("Text = %q, want %q", ev.Text, "hello <@u-2>")
	}
	if len(ev.MentionedUserIDs) != 2 || ev.MentionedUserIDs[0] != "bot-1" {
		t.Errorf("MentionedUserIDs = %v, want [bot-1 u-2]", ev.MentionedUserIDs)
	}
	if ev.SenderIsBot {
		t.Error("SenderIsBot must be false for a human author")
	}
	if len(ev.Raw) == 0 {
		t.Error("Raw must carry the original payload")
	}
}

func TestNormalize_DMHasDMChatTypeAndNoTenant(t *testing.T) {
	dm := `{"id":"m1","channel_id":"dm-1","content":"hi","author":{"id":"u-1"}}`
	ev, err := newTestChannel().Normalize([]byte(dm))
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if ev.ChatType != "dm" {
		t.Errorf("ChatType = %q, want dm (no guild_id)", ev.ChatType)
	}
	if ev.Sender.TenantKey != "" {
		t.Errorf("Sender.TenantKey = %q, want empty for a DM", ev.Sender.TenantKey)
	}
}

func TestNormalize_BotAuthorFlagsSenderIsBot(t *testing.T) {
	bot := `{"id":"m1","channel_id":"c1","guild_id":"g1","content":"x","author":{"id":"b1","bot":true}}`
	ev, err := newTestChannel().Normalize([]byte(bot))
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if !ev.SenderIsBot {
		t.Error("SenderIsBot must be true when author.bot is set")
	}
}

func TestNormalize_NicknameMentionStripped(t *testing.T) {
	// Discord also encodes a mention as "<@!id>" (the nickname form).
	msg := `{"id":"m1","channel_id":"c1","guild_id":"g1","content":"<@!bot-1>   run it","author":{"id":"u1"}}`
	ev, err := newTestChannel().Normalize([]byte(msg))
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if ev.Text != "run it" {
		t.Errorf("Text = %q, want %q", ev.Text, "run it")
	}
}

func TestNormalize_NoAuthorErrors(t *testing.T) {
	if _, err := newTestChannel().Normalize([]byte(`{"id":"m1","channel_id":"c1"}`)); err == nil {
		t.Fatal("Normalize must error on a message with no author")
	}
}

func TestNormalize_MalformedPayloadErrors(t *testing.T) {
	if _, err := newTestChannel().Normalize([]byte("not json")); err == nil {
		t.Fatal("Normalize must error on a malformed payload")
	}
}
