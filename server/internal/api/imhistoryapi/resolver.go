package imhistoryapi

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// tokenSecretLabel domain-separates the history-token signing key from any
// other use of the master key: the effective secret is HMAC(masterKey, label),
// so leaking a history token can never reveal the master key or collide with a
// differently-labelled derivation.
const tokenSecretLabel = "parsar/im-history-token/v1"

// DeriveSecret turns the always-present AES master key into a dedicated
// signing secret for history tokens. It returns "" for an empty master key so
// the caller skips mounting the endpoint rather than signing with a weak key.
func DeriveSecret(masterKey string) string {
	masterKey = strings.TrimSpace(masterKey)
	if masterKey == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(masterKey))
	mac.Write([]byte(tokenSecretLabel))
	return hex.EncodeToString(mac.Sum(nil))
}

// LateResolver defers binding the real HistoryResolver until after the outbound
// worker is constructed — during boot the worker is built later than routes are
// registered. Set is called once, before the HTTP server begins serving, so
// requests always observe a fully-bound resolver. Until Set runs (or if it
// never does) HistoryFetcher reports "not found", which the handler renders as
// an empty page, preserving the tool's never-fail contract.
type LateResolver struct {
	mu    sync.RWMutex
	inner HistoryResolver
}

// Set binds the backing resolver. Safe to call once during boot.
func (l *LateResolver) Set(r HistoryResolver) {
	l.mu.Lock()
	l.inner = r
	l.mu.Unlock()
}

// HistoryFetcher delegates to the bound resolver, or reports not-found when
// unbound.
func (l *LateResolver) HistoryFetcher(ctx context.Context, ref store.ConversationIMRef) (channel.HistoryFetcher, channel.Platform, bool) {
	l.mu.RLock()
	inner := l.inner
	l.mu.RUnlock()
	if inner == nil {
		return nil, "", false
	}
	return inner.HistoryFetcher(ctx, ref)
}
