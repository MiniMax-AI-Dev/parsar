// Package channel — neutral card-action callback contract (PR #3a.3).
//
// When a user clicks a card button the platform delivers an action
// callback. A Channel decodes the raw platform payload into a neutral
// CardAction (HandleAction) and renders the neutral ActionAck the router
// returns back into the platform's native response. The business routing
// (permission verdicts, credential-form submits, user-choice answers) lives
// behind ActionRouter so it stays platform-agnostic and is reused across
// platforms — mirroring OpenClaw (plugin decodes, core routes) and Hermes
// (thin per-adapter handler calling shared approval infra).
package channel

import (
	"context"
	"encoding/json"
)

// CardActionKind classifies a decoded card-button click into a neutral
// category the router dispatches on, independent of the platform wire
// format.
type CardActionKind string

const (
	CardActionUnknown          CardActionKind = ""
	CardActionPermissionAllow  CardActionKind = "permission_allow"
	CardActionPermissionDeny   CardActionKind = "permission_deny"
	CardActionCredentialSubmit CardActionKind = "credential_form_submit"
	CardActionCredentialAck    CardActionKind = "credential_form_acknowledged"
	CardActionUserChoiceSubmit CardActionKind = "ask_user_choice_submit"
	CardActionUserChoicePick   CardActionKind = "ask_user_choice_pick" // legacy pre-form button
)

// CardAction is the neutral descriptor a Channel decodes a raw platform
// action callback into. The neutral router (wired in PR #3c) dispatches on
// Kind and reads Values / FormValues without touching any platform SDK type.
//
// Values carries the button's action-value pairs (request ids etc.), string
// coerced. FormValues carries form-submit values and MAY include
// user-entered secrets (credential forms) — callers MUST NOT log it verbatim.
type CardAction struct {
	Kind              CardActionKind
	Platform          Platform
	BotID             string
	ExternalMessageID string
	ExternalChatID    string
	OperatorID        string
	Values            map[string]string
	FormValues        map[string]any
	Raw               json.RawMessage
}

// ActionAck is the neutral acknowledgement the router returns. The Channel
// renders it into the platform's native callback response (e.g. a Feishu
// toast, optionally replacing the source card).
//
// ReplaceCard and Result are two ways to drive a post-click card swap:
//   - ReplaceCard carries a fully-rendered NATIVE card payload. The Feishu
//     legacy path fills it (its result cards are built inline by the manager)
//     so its callback response stays byte-for-byte identical.
//   - Result is a NEUTRAL result descriptor the Channel renders itself. The
//     router fills it for non-Feishu platforms so the manager never builds a
//     Slack card (fixing the rendering inversion). They are mutually
//     exclusive; a Channel that understands Result renders it, otherwise it
//     falls back to ReplaceCard / the toast.
type ActionAck struct {
	ToastKind    string            // "info" | "success" | "error"; adapter defaults when empty
	ToastContent string            // toast text
	ReplaceCard  json.RawMessage   // optional native card payload to replace the source card (Feishu legacy)
	Result       *ActionResultCard // optional neutral result the Channel renders (non-Feishu)
}

// ActionResultCard is the neutral post-click result the router hands back for
// a Channel to render into its own native result card — the platform-agnostic
// twin of the manager's inline Feishu result cards (permission green/red,
// credential reject/submitted, user-choice done). Only the fields relevant to
// Kind are populated.
type ActionResultCard struct {
	Kind         CardActionKind // which flow produced this result
	Title        string         // card title (agent name); Channel applies its own default when empty
	Approved     bool           // permission verdict: rendered green when true, red when false
	Rejected     bool           // credential submit rejected by the authorization gate
	RejectReason string         // user-facing reason when Rejected (operator / chat mismatch)
	Summary      string         // credential-saved / user-choice "已记录: …" summary line
}

// ActionRouter consumes a decoded, neutral CardAction and returns the
// acknowledgement to render. The production binding (PR #3c) wraps the
// inbound manager's permission / credential-form / user-choice handlers;
// PR #3a.3 ships the seam plus a fake-backed test only.
type ActionRouter interface {
	RouteAction(ctx context.Context, action CardAction) (ActionAck, error)
}
