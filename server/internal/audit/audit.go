// Package audit defines the unified audit infrastructure. The Sink interface
// is a deployment-profile extension point: the open-source core ships a
// Postgres-backed sink writing to audit_records; internal deployments can
// register their own (e.g. ClickHouse / Kafka).
package audit

import (
	"context"
	"time"
)

// Source enumerates the top-level audit categories. The Postgres schema
// constrains the column to exactly these values.
const (
	SourceIdentity = "identity"
	SourceAdmin    = "admin"
	SourceRuntime  = "runtime"
	SourceApproval = "approval"
	SourceData     = "data"
)

// ActorType enumerates the audit-record actor classes. The Postgres schema
// constrains the column to exactly these values.
const (
	ActorTypeUser     = "user"
	ActorTypeAgent    = "agent"
	ActorTypeSystem   = "system"
	ActorTypeExternal = "external"
)

// Event is a normalized audit event ready to be persisted by a Sink.
// Empty string IDs translate to SQL NULL. OccurredAt defaults to time.Now()
// when left zero by the caller.
type Event struct {
	OccurredAt  time.Time
	Source      string         // one of Source* constants
	EventType   string         // e.g. "model.created", "agent_run.started"
	ActorType   string         // one of ActorType* constants
	ActorID     string         // empty => NULL
	TargetType  string         // free-form, e.g. "model", "agent_run"
	TargetID    string         // empty => NULL
	WorkspaceID string         // empty => NULL
	Payload     map[string]any // marshalled to jsonb
}

// Sink persists a single audit event. Implementations must be goroutine-safe.
// A Sink must not retry internally — long-running retries inside Write
// block the ingester worker and pin the buffer.
type Sink interface {
	Write(ctx context.Context, ev Event) error
}

// Stats captures observable health of the ingester pipeline.
type Stats struct {
	// Emitted counts events the ingester accepted into its buffer
	// (including those that later failed in the sink).
	Emitted int64
	// Dropped counts events rejected because the buffer was full at Emit().
	// Primary "we are losing audit data" signal.
	Dropped int64
	// SinkErrors counts events for which Sink.Write returned non-nil.
	SinkErrors int64
	// LastLagNanos is the most recent observed delay between Emit() and
	// the worker picking the event off the buffer.
	LastLagNanos int64
	BufferLen    int
	BufferCap    int
}
