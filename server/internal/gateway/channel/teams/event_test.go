package teams

import (
	"reflect"
	"testing"
)

// dmActivity is a 1:1 ("personal") message — no @mention required downstream.
const dmActivity = `{
  "type":"message","id":"m1","text":"hello there","locale":"en-US",
  "serviceUrl":"https://smba.trafficmanager.net/amer/","channelId":"msteams",
  "from":{"id":"29:user-a","name":"Alice","aadObjectId":"aad-alice","role":"user"},
  "recipient":{"id":"28:app-123"},
  "conversation":{"id":"conv-dm","conversationType":"personal","tenantId":"tenant-7"}
}`

// channelMention is a team-channel message that @mentions the bot; the <at> tag
// is stripped from Text and the bot id lands in MentionedUserIDs.
const channelMention = `{
  "type":"message","id":"m2","text":"<at>Bot</at> deploy please",
  "serviceUrl":"https://smba.trafficmanager.net/emea/","channelId":"msteams",
  "from":{"id":"29:user-b","name":"Bob","aadObjectId":"aad-bob"},
  "recipient":{"id":"28:app-123"},
  "conversation":{"id":"conv-ch;messageid=root-1","conversationType":"channel","tenantId":"tenant-7"},
  "entities":[{"type":"mention","text":"<at>Bot</at>","mentioned":{"id":"28:app-123","name":"Bot"}}],
  "channelData":{"tenant":{"id":"tenant-cd"}}
}`

func TestNormalize_DM(t *testing.T) {
	ev, err := newTestChannel().Normalize([]byte(dmActivity))
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if ev.Platform != "teams" {
		t.Errorf("Platform = %q, want teams", ev.Platform)
	}
	if ev.ChatType != "dm" {
		t.Errorf("ChatType = %q, want dm (personal)", ev.ChatType)
	}
	if ev.BotID != "28:app-123" {
		t.Errorf("BotID = %q, want 28:app-123 (recipient)", ev.BotID)
	}
	if ev.ExternalChatID != "conv-dm" || ev.ExternalRootID != "conv-dm" {
		t.Errorf("ChatID/RootID = %q/%q, want conv-dm/conv-dm", ev.ExternalChatID, ev.ExternalRootID)
	}
	if ev.Sender.PlatformUserID != "aad-alice" {
		t.Errorf("PlatformUserID = %q, want aad-alice (aadObjectId preferred)", ev.Sender.PlatformUserID)
	}
	if ev.Sender.LocalUserID != "29:user-a" {
		t.Errorf("LocalUserID = %q, want 29:user-a", ev.Sender.LocalUserID)
	}
	if ev.Sender.TenantKey != "tenant-7" {
		t.Errorf("TenantKey = %q, want tenant-7", ev.Sender.TenantKey)
	}
	if ev.Text != "hello there" {
		t.Errorf("Text = %q, want hello there", ev.Text)
	}
	if ev.Metadata["service_url"] != "https://smba.trafficmanager.net/amer/" {
		t.Errorf("metadata service_url = %v", ev.Metadata["service_url"])
	}
	if ev.Metadata["locale"] != "en-US" {
		t.Errorf("metadata locale = %v, want en-US", ev.Metadata["locale"])
	}
}

func TestNormalize_ChannelMention(t *testing.T) {
	ev, err := newTestChannel().Normalize([]byte(channelMention))
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if ev.ChatType != "channel" {
		t.Errorf("ChatType = %q, want channel", ev.ChatType)
	}
	if ev.Text != "deploy please" {
		t.Errorf("Text = %q, want mention-stripped 'deploy please'", ev.Text)
	}
	if want := []string{"28:app-123"}; !reflect.DeepEqual(ev.MentionedUserIDs, want) {
		t.Errorf("MentionedUserIDs = %v, want %v", ev.MentionedUserIDs, want)
	}
	// channelData tenant is the fallback; conversation.tenantId wins when present.
	if ev.Sender.TenantKey != "tenant-7" {
		t.Errorf("TenantKey = %q, want tenant-7 (conversation wins over channelData)", ev.Sender.TenantKey)
	}
}

func TestNormalize_RejectsNonMessage(t *testing.T) {
	for _, payload := range []string{
		`{"type":"conversationUpdate","id":"c1"}`,
		`{"type":"invoke","id":"i1"}`,
		`not json`,
	} {
		if _, err := newTestChannel().Normalize([]byte(payload)); err == nil {
			t.Errorf("Normalize(%q) = nil error, want a skip error", payload)
		}
	}
}

func TestTeamsChatType(t *testing.T) {
	cases := map[string]struct {
		conv conversation
		want string
	}{
		"personal":         {conversation{ConversationType: "personal"}, "dm"},
		"channel":          {conversation{ConversationType: "channel"}, "channel"},
		"groupChat":        {conversation{ConversationType: "groupChat"}, "group"},
		"isGroup fallback": {conversation{IsGroup: true}, "group"},
		"empty default":    {conversation{}, "dm"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := teamsChatType(tc.conv); got != tc.want {
				t.Errorf("teamsChatType = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTeamsSenderIsBot(t *testing.T) {
	if !teamsSenderIsBot(channelAccount{Role: "bot"}) {
		t.Error("role=bot must be a bot")
	}
	if !teamsSenderIsBot(channelAccount{ID: "28:app-123"}) {
		t.Error("28:-prefixed id must be a bot")
	}
	if teamsSenderIsBot(channelAccount{ID: "29:user-a", Role: "user"}) {
		t.Error("a human 29: account must not be a bot")
	}
}

func TestConversationRefFrom(t *testing.T) {
	convID, ref, ok := conversationRefFrom([]byte(channelMention))
	if !ok {
		t.Fatal("expected a ref")
	}
	if convID != "conv-ch;messageid=root-1" {
		t.Errorf("convID = %q, want the verbatim conversation id", convID)
	}
	if ref.ServiceURL != "https://smba.trafficmanager.net/emea/" {
		t.Errorf("ServiceURL = %q", ref.ServiceURL)
	}
	if ref.BotAppID != "28:app-123" {
		t.Errorf("BotAppID = %q", ref.BotAppID)
	}
	// channelData tenant is the fallback when conversation.tenantId is present.
	if ref.TenantID != "tenant-7" {
		t.Errorf("TenantID = %q, want tenant-7", ref.TenantID)
	}
	if _, _, ok := conversationRefFrom([]byte(`{"type":"message"}`)); ok {
		t.Error("no conversation id must yield ok=false")
	}
}
