package teams

import (
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// newTestChannel builds an adapter with a fixed App Id (verification disabled —
// no verifier is wired, so Verify is a pass-through, matching the Emulator path)
// for the decode/render tests that never cross the network.
func newTestChannel() *Channel {
	return New(Config{AppID: "app-123"})
}

func TestNew_BotLocalID(t *testing.T) {
	c := newTestChannel()
	if got := c.BotLocalID(); got != "28:app-123" {
		t.Errorf("BotLocalID = %q, want 28:app-123", got)
	}
	if got := New(Config{}).BotLocalID(); got != "" {
		t.Errorf("empty AppID must yield empty BotLocalID, got %q", got)
	}
}

func TestPlatformAndCapabilities(t *testing.T) {
	c := newTestChannel()
	if c.Platform() != channel.PlatformTeams {
		t.Errorf("Platform = %q, want teams", c.Platform())
	}
	caps := c.Capabilities()
	if !caps.Edit {
		t.Error("Teams supports PUT activity edit; Edit must be true")
	}
	if caps.BlockStreaming {
		t.Error("BlockStreaming must stay false (Connector rate limits)")
	}
	if !caps.Reply || !caps.Threads {
		t.Error("Reply and Threads must be true")
	}
	if caps.MaxMessageLen != teamsMaxMessageLen {
		t.Errorf("MaxMessageLen = %d, want %d", caps.MaxMessageLen, teamsMaxMessageLen)
	}
}

// TestRememberInbound_PrimesConversationStore proves the runner-called priming
// hook extracts the serviceUrl/tenant/bot from a raw activity and caches it
// keyed by conversation id — the outbound path's only route to the regional
// Connector, since ReplyTarget carries no serviceUrl slot.
func TestRememberInbound_PrimesConversationStore(t *testing.T) {
	store := NewMemoryConversationStore()
	c := New(Config{AppID: "app-123"}, WithConversationStore(store))

	const inbound = `{
	  "type":"message","id":"a1","serviceUrl":"https://smba.trafficmanager.net/amer/",
	  "recipient":{"id":"28:app-123"},
	  "conversation":{"id":"conv-9","tenantId":"tenant-7"}
	}`
	convID := c.RememberInbound([]byte(inbound))
	if convID != "conv-9" {
		t.Fatalf("RememberInbound returned %q, want conv-9", convID)
	}
	ref, ok := store.Get("conv-9")
	if !ok {
		t.Fatal("conversation ref not cached")
	}
	if ref.ServiceURL != "https://smba.trafficmanager.net/amer/" {
		t.Errorf("ServiceURL = %q", ref.ServiceURL)
	}
	if ref.TenantID != "tenant-7" {
		t.Errorf("TenantID = %q, want tenant-7", ref.TenantID)
	}
	if ref.BotAppID != "28:app-123" {
		t.Errorf("BotAppID = %q, want 28:app-123", ref.BotAppID)
	}
}

// TestRememberInbound_NoConversationIsNoop: a payload with no conversation id
// caches nothing and returns "".
func TestRememberInbound_NoConversationIsNoop(t *testing.T) {
	c := newTestChannel()
	if got := c.RememberInbound([]byte(`{"type":"message","id":"a1"}`)); got != "" {
		t.Errorf("RememberInbound with no conversation = %q, want empty", got)
	}
	if got := c.RememberInbound([]byte("not json")); got != "" {
		t.Errorf("RememberInbound on malformed = %q, want empty", got)
	}
}
