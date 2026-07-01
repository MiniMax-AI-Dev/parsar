// Package discord — component pick accumulation (PR #5c).
//
// Discord's interaction model differs from Slack's in one way that shapes the
// user-choice flow: a string select fires its OWN INTERACTION_CREATE on every
// change, and a later button click does NOT echo the other components' current
// state (Slack delivers the whole view's state.values with the submit). So to
// collect a multi-question choice form, the picks must be remembered between the
// select interactions and the final Submit click.
//
// ComponentPickStore is that memory. It is owned by the inbound runner (not the
// pure adapter) and injected via WithPickStore, mirroring how the Slack runner
// owns its dedup set: the adapter stays a decoder, the live-socket state lives
// outside it. HandleAction records each select pick and drains the accumulated
// picks into FormValues when the Submit button arrives.
package discord

import (
	"strings"
	"sync"
)

// ComponentPickStore accumulates a card's string-select picks across the
// separate interactions Discord delivers, keyed by the source message id, until
// the Submit button drains them. Implementations must be safe for concurrent
// use — the runner dispatches interactions from the gateway read loop.
type ComponentPickStore interface {
	// Record stores question questionIdx's picked option values for messageID,
	// replacing any prior pick for that question (a re-pick wins).
	Record(messageID, questionIdx string, values []string)
	// Drain returns the accumulated picks for messageID as the neutral
	// FormValues shape the user-choice router reads — keyed "q<idx>", a single
	// pick as a string and a multi-pick as a []any of strings — and clears them.
	// Returns nil when nothing was recorded.
	Drain(messageID string) map[string]any
}

// MemoryPickStore is the default in-memory ComponentPickStore. It bounds memory
// by clearing a message's picks on Drain (the submit), so only in-flight forms
// occupy space; an abandoned form's picks are reclaimed when the bound is hit.
type MemoryPickStore struct {
	mu      sync.Mutex
	byMsg   map[string]map[string][]string
	maxMsgs int
}

// NewMemoryPickStore builds an empty in-memory pick store.
func NewMemoryPickStore() *MemoryPickStore {
	return &MemoryPickStore{byMsg: map[string]map[string][]string{}, maxMsgs: 4096}
}

// Record stores a question's picked values for a message, replacing any prior
// pick for that question. An empty messageID or questionIdx is ignored.
func (s *MemoryPickStore) Record(messageID, questionIdx string, values []string) {
	messageID = strings.TrimSpace(messageID)
	questionIdx = strings.TrimSpace(questionIdx)
	if messageID == "" || questionIdx == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.byMsg) >= s.maxMsgs {
		// A pick-storm with no submits: drop the whole window rather than grow
		// unbounded. In-flight forms re-pick on the next interaction.
		s.byMsg = map[string]map[string][]string{}
	}
	q := s.byMsg[messageID]
	if q == nil {
		q = map[string][]string{}
		s.byMsg[messageID] = q
	}
	q[questionIdx] = append([]string(nil), values...)
}

// Drain returns a message's accumulated picks in the neutral FormValues shape and
// clears them. A single-value pick lands as a string, a multi-value pick as a
// []any of strings — the shape the user-choice router reads per question index.
func (s *MemoryPickStore) Drain(messageID string) map[string]any {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	q, ok := s.byMsg[messageID]
	if !ok {
		return nil
	}
	delete(s.byMsg, messageID)
	if len(q) == 0 {
		return nil
	}
	out := make(map[string]any, len(q))
	for idx, values := range q {
		picked := make([]string, 0, len(values))
		for _, v := range values {
			if v = strings.TrimSpace(v); v != "" {
				picked = append(picked, v)
			}
		}
		if len(picked) == 0 {
			continue
		}
		key := "q" + idx
		if len(picked) == 1 {
			out[key] = picked[0]
			continue
		}
		anyPicks := make([]any, len(picked))
		for i, v := range picked {
			anyPicks[i] = v
		}
		out[key] = anyPicks
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
