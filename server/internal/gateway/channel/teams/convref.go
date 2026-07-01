package teams

import (
	"strings"
	"sync"
)

// botLocalIDFor builds the bot's Teams channel-account id from its Microsoft
// App Id. Teams stamps a bot's account id as "28:<appId>" on the recipient of
// an inbound activity and inside a user's @mention entity, so the mention gate
// matches MentionedUserIDs against this form. Empty appId yields "".
func botLocalIDFor(appID string) string {
	appID = strings.TrimSpace(appID)
	if appID == "" {
		return ""
	}
	return "28:" + appID
}

// trim is the package-local strings.TrimSpace shorthand used across the
// adapter's field assignments.
func trim(s string) string { return strings.TrimSpace(s) }

// ConversationRef is the per-conversation routing context the outbound path
// needs but the neutral ReplyTarget cannot carry. serviceURL is the regional
// Bot Framework Connector base URL (e.g. https://smba.trafficmanager.net/amer/)
// the reply POST targets; TenantID scopes a proactive send (Teams rejects a
// send with no tenant on a channel conversation); BotAppID is the recipient bot
// captured inbound so a multi-bot deployment resolves the right credential.
type ConversationRef struct {
	ServiceURL string
	TenantID   string
	BotAppID   string
}

// ConversationStore caches the ConversationRef for a conversation id. The runner
// primes it on each inbound (the serviceUrl/tenant ride the inbound Activity);
// the outbound transport reads it back to address the Connector. An in-memory
// implementation suffices for the inbound-only path (a synchronous command
// reply happens in the same request that primed the ref); a persistent
// implementation is injectable via WithConversationStore for proactive/async
// sends across replicas.
type ConversationStore interface {
	Put(conversationID string, ref ConversationRef)
	Get(conversationID string) (ConversationRef, bool)
}

// memoryConversationStore is the default in-process ConversationStore. It is
// safe for concurrent webhook goroutines. It never evicts: conversation ids are
// low-cardinality relative to message volume, and a restart re-primes on the
// next inbound.
type memoryConversationStore struct {
	mu   sync.RWMutex
	refs map[string]ConversationRef
}

// NewMemoryConversationStore builds the default in-memory conversation-reference
// cache.
func NewMemoryConversationStore() ConversationStore {
	return &memoryConversationStore{refs: make(map[string]ConversationRef)}
}

func (s *memoryConversationStore) Put(conversationID string, ref ConversationRef) {
	id := strings.TrimSpace(conversationID)
	if id == "" {
		return
	}
	s.mu.Lock()
	s.refs[id] = ref
	s.mu.Unlock()
}

func (s *memoryConversationStore) Get(conversationID string) (ConversationRef, bool) {
	id := strings.TrimSpace(conversationID)
	if id == "" {
		return ConversationRef{}, false
	}
	s.mu.RLock()
	ref, ok := s.refs[id]
	s.mu.RUnlock()
	return ref, ok
}
