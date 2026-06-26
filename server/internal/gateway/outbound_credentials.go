// Package gateway: shared types for the outbound credential resolver
// (consumed by the Feishu inflight driver in feishuoutbound).
package gateway

import (
	"context"
	"errors"
	"time"
)

// PendingOutboundMessage is the projection the credential resolver reads.
// Lives gateway-side so the package doesn't depend on store.
type PendingOutboundMessage struct {
	MessageID        string
	WorkspaceID      string
	ProjectID        string
	ConversationID   string
	Text             string
	Gateway          string
	ExternalChatID   string
	ExternalThreadID string
	SourceAppID      string
	RetryCount       int

	Metadata  map[string]any
	CreatedAt time.Time
}

// OutboundCredentials carries the per-Agent secrets resolved from the
// vault. AppSecret is sensitive; treat as write-only after construction.
type OutboundCredentials struct {
	AppID     string
	AppSecret string
}

// CredentialResolver resolves the Feishu Bot credentials for a single
// pending message. Implementations look up the Agent by SourceAppID,
// decode the Feishu connector config, read the encrypted payload via
// app_secret_ref, and decrypt with the workspace's secrets.Service.
//
// ErrUnresolvableOutbound marks the message as permanently undeliverable —
// the driver dead-letters it without consuming a retry slot.
type CredentialResolver func(ctx context.Context, msg PendingOutboundMessage) (OutboundCredentials, error)

// ErrUnresolvableOutbound: source_app_id no longer maps to a live Agent
// (deleted, soft-disabled, or never registered). Driver dead-letters.
var ErrUnresolvableOutbound = errors.New("outbound message cannot be resolved to a live agent")
