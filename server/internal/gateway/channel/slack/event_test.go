package slack

import (
	"testing"
)

// appMentionEnvelope is an event_callback wrapping an app_mention inner event,
// the bot's primary trigger.
const appMentionEnvelope = `{
  "type":"event_callback",
  "team_id":"T1",
  "api_app_id":"A123",
  "event":{
    "type":"app_mention",
    "user":"U1",
    "text":"<@A123> deploy please",
    "ts":"1700000000.000100",
    "thread_ts":"1699999999.000000",
    "channel":"C1",
    "event_ts":"1700000000.000100"
  }
}`

// messageEnvelope is an event_callback wrapping a direct-message message event.
const messageEnvelope = `{
  "type":"event_callback",
  "team_id":"T2",
  "api_app_id":"A123",
  "event":{
    "type":"message",
    "user":"U2",
    "text":"hello",
    "ts":"1700000001.000200",
    "channel":"D9",
    "channel_type":"im",
    "event_ts":"1700000001.000200"
  }
}`

func TestNormalize_AppMention(t *testing.T) {
	ev, err := newTestChannel().Normalize([]byte(appMentionEnvelope))
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if ev.Platform != "slack" {
		t.Errorf("Platform = %q, want slack", ev.Platform)
	}
	if ev.BotID != "A123" {
		t.Errorf("BotID = %q, want A123 (api_app_id)", ev.BotID)
	}
	if ev.ExternalMessageID != "1700000000.000100" {
		t.Errorf("ExternalMessageID = %q, want the message ts", ev.ExternalMessageID)
	}
	if ev.ExternalChatID != "C1" {
		t.Errorf("ExternalChatID = %q, want C1", ev.ExternalChatID)
	}
	if ev.ExternalThreadID != "1699999999.000000" {
		t.Errorf("ExternalThreadID = %q, want the thread_ts", ev.ExternalThreadID)
	}
	if ev.ReplyTo != "1699999999.000000" {
		t.Errorf("ReplyTo = %q, want the thread anchor", ev.ReplyTo)
	}
	if ev.Sender.PlatformUserID != "U1" {
		t.Errorf("Sender = %q, want U1", ev.Sender.PlatformUserID)
	}
	if ev.Sender.TenantKey != "T1" {
		t.Errorf("TenantKey = %q, want T1 (team_id)", ev.Sender.TenantKey)
	}
	if ev.Text != "deploy please" {
		t.Errorf("Text = %q, want the mention-stripped text", ev.Text)
	}
	if len(ev.Raw) == 0 {
		t.Error("Raw must carry the original payload")
	}
}

func TestNormalize_StripsLeadingMentionForCommands(t *testing.T) {
	// A channel command arrives as "<@BOT> /list"; the leading mention must be
	// stripped so the router's command parser (which requires a leading "/")
	// sees "/list". This is the bug that made /list/​/select unreachable on Slack.
	const env = `{
  "type":"event_callback","team_id":"T1","api_app_id":"A123",
  "event":{"type":"app_mention","user":"U1","text":"<@U0BDSG4A5FE> /list",
    "ts":"1700000000.000100","channel":"C1","event_ts":"1700000000.000100"}
}`
	ev, err := newTestChannel().Normalize([]byte(env))
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if ev.Text != "/list" {
		t.Errorf("Text = %q, want /list (leading mention stripped)", ev.Text)
	}
	if len(ev.MentionedUserIDs) != 1 || ev.MentionedUserIDs[0] != "U0BDSG4A5FE" {
		t.Errorf("MentionedUserIDs = %v, want [U0BDSG4A5FE] (still parsed from raw)", ev.MentionedUserIDs)
	}
}

func TestNormalize_DirectMessage(t *testing.T) {
	ev, err := newTestChannel().Normalize([]byte(messageEnvelope))
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if ev.ExternalChatID != "D9" {
		t.Errorf("ExternalChatID = %q, want D9 (dm)", ev.ExternalChatID)
	}
	if ev.Sender.PlatformUserID != "U2" {
		t.Errorf("Sender = %q, want U2", ev.Sender.PlatformUserID)
	}
	if ev.Sender.TenantKey != "T2" {
		t.Errorf("TenantKey = %q, want T2", ev.Sender.TenantKey)
	}
	// No thread_ts on a top-level DM.
	if ev.ExternalThreadID != "" {
		t.Errorf("ExternalThreadID = %q, want empty for a top-level DM", ev.ExternalThreadID)
	}
}

func TestNormalize_UnsupportedEventErrors(t *testing.T) {
	// reaction_added is a real Slack event we do not map; it must error so the
	// caller skips it rather than producing an empty InboundEvent.
	const reaction = `{"type":"event_callback","team_id":"T1","api_app_id":"A123","event":{"type":"reaction_added","user":"U1","reaction":"thumbsup","event_ts":"1.2"}}`
	if _, err := newTestChannel().Normalize([]byte(reaction)); err == nil {
		t.Fatal("Normalize must error on an unsupported inner event")
	}
}

func TestNormalize_MalformedPayloadErrors(t *testing.T) {
	if _, err := newTestChannel().Normalize([]byte("not json")); err == nil {
		t.Fatal("Normalize must error on a malformed payload")
	}
}
