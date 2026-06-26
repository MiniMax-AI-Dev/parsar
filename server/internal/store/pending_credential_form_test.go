package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Pure-unit tests for the pending_credential_form slot wrappers. The
// DB-touching paths are covered by integration tests in store_test.go
// / handle_card_action_test.go; this file pins the wire shape and
// guard rails that don't need a Postgres instance to verify.

// TestPendingCredentialForm_MintQkeyPrefixAndEntropy locks in the wire
// shape of the qkey: a recognisable "qkey_" prefix plus 32 hex chars
// of entropy. Logs / chat operators rely on the prefix to spot a
// leaked stash key at a glance; brute force is impractical at this
// length within the 1-hour TTL window.
func TestPendingCredentialForm_MintQkeyPrefixAndEntropy(t *testing.T) {
	seen := make(map[string]struct{})
	for i := 0; i < 32; i++ {
		got, err := MintFeishuCredentialQkey()
		if err != nil {
			t.Fatalf("mint: %v", err)
		}
		if !strings.HasPrefix(got, FeishuCredentialQkeyPrefix) {
			t.Fatalf("missing prefix %q in %q", FeishuCredentialQkeyPrefix, got)
		}
		body := strings.TrimPrefix(got, FeishuCredentialQkeyPrefix)
		if len(body) != 32 {
			t.Fatalf("entropy body wrong length: got %d, want 32", len(body))
		}
		for _, b := range body {
			if !(('0' <= b && b <= '9') || ('a' <= b && b <= 'f')) {
				t.Fatalf("body must be lowercase hex, got %q", body)
			}
		}
		if _, dup := seen[got]; dup {
			t.Fatalf("duplicate qkey after %d mints — entropy too low", i)
		}
		seen[got] = struct{}{}
	}
}

// TestPendingCredentialForm_ExpiresAtDefault_Sane documents the 1-hour
// default expiry the store fills in for slots that pass a zero
// ExpiresAt — the schema previously enforced this via column
// DEFAULT, but the slot version replaces the column with a Go-side
// fallback so the guard is now structural code that future refactors
// could easily shift unnoticed.
func TestPendingCredentialForm_ExpiresAtDefault_Sane(t *testing.T) {
	slot := PendingCredentialFormSlot{}
	if !slot.ExpiresAt.IsZero() {
		t.Fatal("default-constructed slot should have zero ExpiresAt")
	}
	computed := time.Now().UTC().Add(time.Hour)
	diff := computed.Sub(time.Now().UTC())
	if diff < 50*time.Minute {
		t.Fatalf("default expires_at window %v < 50m — likely a typo on the hour multiplier", diff)
	}
	if diff > 70*time.Minute {
		t.Fatalf("default expires_at window %v > 70m — likely a typo on the hour multiplier", diff)
	}
}

// TestPendingCredentialForm_WriteRejectsEmptyRequiredFields locks the
// runtime guards inside WritePendingCredentialFormSlot. Each missing
// field has a specific reason it must not be silently empty:
//
//   - Qkey: callback's only lookup handle; an empty key is unrecoverable
//   - InitiatorOpenID: authz check compares against
//     callback.Operator.OpenID — empty here degrades the check into a no-op
//   - InitiatorUserID: write target of ReplaceUserCredentials; an empty
//     value would either fail the UUID parse or worse, write under an
//     ambiguous owner
//   - RawQuery: the entire point of the stash is to replay this; empty
//     means "nothing to resume" — submit handler would silently no-op
//
// Without these guards a regression upstream that drops one of the
// fields fails only at integration test time; the pure-unit checks
// here surface the issue immediately.
func TestPendingCredentialForm_WriteRejectsEmptyRequiredFields(t *testing.T) {
	// Store with nil db is fine: every validation runs BEFORE we touch
	// the DB, so the request fails fast on the empty-string check.
	s := &Store{}
	base := PendingCredentialFormSlot{
		Qkey:            "qkey_test",
		InitiatorOpenID: "ou_alice",
		InitiatorUserID: "33333333-3333-3333-3333-333333333333",
		RawQuery:        "list my open PRs",
	}
	convID := "44444444-4444-4444-4444-444444444444"
	cases := []struct {
		name      string
		mutate    func(*PendingCredentialFormSlot)
		errSubstr string
	}{
		{
			name:      "empty qkey",
			mutate:    func(s *PendingCredentialFormSlot) { s.Qkey = "" },
			errSubstr: "qkey is required",
		},
		{
			name:      "whitespace qkey",
			mutate:    func(s *PendingCredentialFormSlot) { s.Qkey = "   " },
			errSubstr: "qkey is required",
		},
		{
			name:      "empty initiator_open_id",
			mutate:    func(s *PendingCredentialFormSlot) { s.InitiatorOpenID = "" },
			errSubstr: "initiator_open_id is required",
		},
		{
			name:      "whitespace initiator_open_id",
			mutate:    func(s *PendingCredentialFormSlot) { s.InitiatorOpenID = "   " },
			errSubstr: "initiator_open_id is required",
		},
		{
			name:      "empty initiator_user_id",
			mutate:    func(s *PendingCredentialFormSlot) { s.InitiatorUserID = "" },
			errSubstr: "initiator_user_id is required",
		},
		{
			name:      "empty raw_query",
			mutate:    func(s *PendingCredentialFormSlot) { s.RawQuery = "" },
			errSubstr: "raw_query is required",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			slot := base
			tc.mutate(&slot)
			_, err := s.WritePendingCredentialFormSlot(context.Background(), convID, slot)
			if err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.errSubstr) {
				t.Fatalf("error must cite %q; got %q", tc.errSubstr, err.Error())
			}
		})
	}
}

// TestPendingCredentialForm_DecodeSlot_ExternalMsgID asserts the new
// external_msg_id field round-trips through the decoder when it's
// present in the jsonb payload, and is zero on legacy payloads that
// predate the column (the insert-or-noop + patch-on-second-tick fix
// relies on this field to decide between Send and Patch on the next
// inflight tick).
func TestPendingCredentialForm_DecodeSlot_ExternalMsgID(t *testing.T) {
	t.Run("present in payload", func(t *testing.T) {
		raw := `{"qkey":"qkey_abc","external_msg_id":"om_card_1","initiator_open_id":"ou_bob","initiator_user_id":"u-1","raw_query":"hi","expires_at":"2026-12-31T23:59:59Z"}`
		got := decodePendingCredentialFormSlot(raw)
		if got.ExternalMsgID != "om_card_1" {
			t.Errorf("ExternalMsgID = %q, want %q", got.ExternalMsgID, "om_card_1")
		}
		if got.Qkey != "qkey_abc" {
			t.Errorf("Qkey = %q, want %q", got.Qkey, "qkey_abc")
		}
	})
	t.Run("absent on legacy payload", func(t *testing.T) {
		raw := `{"qkey":"qkey_abc","initiator_open_id":"ou_bob","initiator_user_id":"u-1","raw_query":"hi","expires_at":"2026-12-31T23:59:59Z"}`
		got := decodePendingCredentialFormSlot(raw)
		if got.ExternalMsgID != "" {
			t.Errorf("legacy payload must decode ExternalMsgID = empty; got %q", got.ExternalMsgID)
		}
	})
}

// TestPendingCredentialForm_UpdateMessageID_RejectsEmptyFields locks
// the validation guards on the new wrapper before it hits the DB —
// passing an empty qkey would let the SQL match the first slot it
// finds on the conversation (predicate degenerates to `'' = ''`),
// stamping the wrong card. Tests for the actual SQL semantics live
// in store_test.go's integration suite.
func TestPendingCredentialForm_UpdateMessageID_RejectsEmptyFields(t *testing.T) {
	s := &Store{}
	convID := "44444444-4444-4444-4444-444444444444"
	cases := []struct {
		name          string
		qkey          string
		externalMsgID string
		errSubstr     string
	}{
		{name: "empty qkey", qkey: "", externalMsgID: "om_x", errSubstr: "qkey is required"},
		{name: "whitespace qkey", qkey: "  ", externalMsgID: "om_x", errSubstr: "qkey is required"},
		{name: "empty external_msg_id", qkey: "qkey_x", externalMsgID: "", errSubstr: "external_msg_id is required"},
		{name: "whitespace external_msg_id", qkey: "qkey_x", externalMsgID: "  ", errSubstr: "external_msg_id is required"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := s.UpdatePendingCredentialFormSlotMessageID(context.Background(), convID, tc.qkey, tc.externalMsgID)
			if err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.errSubstr) {
				t.Fatalf("error must cite %q; got %q", tc.errSubstr, err.Error())
			}
		})
	}
}
// inflight slot's decoder tolerance: pgx may surface jsonb as nil /
// []byte / string / map[string]any depending on the column path and
// driver version. The decoder must absorb all four without panicking
// (a zero-value slot is the "couldn't decode" outcome, which the
// claim path then treats as ErrPendingCredentialFormNotFound through
// the SQL filter rather than this Go code).
func TestPendingCredentialForm_DecodeSlot_TolerantOfShapes(t *testing.T) {
	payload := PendingCredentialFormSlot{
		Qkey:            "qkey_abc",
		InitiatorOpenID: "ou_bob",
		InitiatorUserID: "55555555-5555-5555-5555-555555555555",
		RawQuery:        "hello",
		ExpiresAt:       time.Now().UTC().Truncate(time.Second),
	}
	jsonStr := `{"qkey":"qkey_abc","initiator_open_id":"ou_bob","initiator_user_id":"55555555-5555-5555-5555-555555555555","raw_query":"hello","expires_at":"` + payload.ExpiresAt.Format(time.RFC3339Nano) + `"}`
	cases := []struct {
		name string
		raw  any
	}{
		{name: "nil raw", raw: nil},
		{name: "[]byte raw", raw: []byte(jsonStr)},
		{name: "string raw", raw: jsonStr},
		{name: "map raw", raw: map[string]any{
			"qkey":              payload.Qkey,
			"initiator_open_id": payload.InitiatorOpenID,
			"initiator_user_id": payload.InitiatorUserID,
			"raw_query":         payload.RawQuery,
			"expires_at":        payload.ExpiresAt.Format(time.RFC3339Nano),
		}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("decoder panicked on %s: %v", tc.name, r)
				}
			}()
			_ = decodePendingCredentialFormSlot(tc.raw)
		})
	}
}
