package otlp

import (
	"fmt"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/audit"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// Parsar attribute keys carried on OTLP spans. Producers MUST set
// these; missing required keys cause record rejection.
const (
	AttrToolCallID    = "parsar.tool_call.id"
	AttrToolCallAct   = "parsar.tool_call.action"
	AttrRequester     = "parsar.requester"
	AttrExecutor      = "parsar.executor"
	AttrWorkspaceID   = "parsar.workspace_id"
	AttrProjectID     = "parsar.project_id"
	AttrApprover      = "parsar.approver"
	AttrCredentialID  = "parsar.credential.id"
	AttrExternalURL   = "parsar.external_object.url"
	AttrRequesterType = "parsar.requester.type" // "user" / "agent" (default "user")
)

// ToolCallEventPrefix gates which SpanEvents the receiver recognizes:
// `tool_call.started`, `tool_call.approval_required`,
// `tool_call.approved`, `tool_call.denied`,
// `tool_call.credential_delivered`, `tool_call.completed`,
// `tool_call.failed`.
const ToolCallEventPrefix = "tool_call."

var eventTypesRequiringApprover = map[string]struct{}{
	"tool_call.approved": {},
	"tool_call.denied":   {},
}

var eventTypesRequiringCredential = map[string]struct{}{
	"tool_call.credential_delivered": {},
}

// convertTraces walks an OTLP ExportTraceServiceRequest and converts
// each recognized SpanEvent into an audit.Event. Returns the converted
// events plus per-record schema-validation errors. Ingester buffer
// rejections are tracked separately by the caller.
func convertTraces(req *coltracepb.ExportTraceServiceRequest) ([]audit.Event, []error) {
	var (
		events []audit.Event
		errs   []error
	)
	for _, rs := range req.GetResourceSpans() {
		resourceAttrs := flattenAttrs(rs.GetResource().GetAttributes())
		for _, ss := range rs.GetScopeSpans() {
			for _, span := range ss.GetSpans() {
				spanAttrs := mergeAttrs(resourceAttrs, flattenAttrs(span.GetAttributes()))
				spanEvents, spanErrs := convertSpan(span, spanAttrs)
				events = append(events, spanEvents...)
				errs = append(errs, spanErrs...)
			}
		}
	}
	return events, errs
}

// convertSpan emits one audit.Event per recognized SpanEvent on the
// span. Span attributes are inherited by every emitted Event, so
// producers can declare workspace / requester / executor once on the
// span rather than repeating them per event.
func convertSpan(span *tracepb.Span, spanAttrs map[string]string) ([]audit.Event, []error) {
	var (
		out  []audit.Event
		errs []error
	)
	for _, sev := range span.GetEvents() {
		name := sev.GetName()
		if name == "" || !hasPrefix(name, ToolCallEventPrefix) {
			// Silently ignore unrelated events so producers can
			// piggyback non-Parsar span events without
			// triggering schema warnings.
			continue
		}

		// Per-event attributes override span-level attributes so
		// producers can declare baseline context on the span and
		// only override per-event details (credential id, external
		// object URL, …).
		merged := mergeAttrs(spanAttrs, flattenAttrs(sev.GetAttributes()))

		ev, err := buildEvent(name, sev.GetTimeUnixNano(), merged)
		if err != nil {
			errs = append(errs, fmt.Errorf("span %x event %q: %w",
				span.GetSpanId(), name, err))
			continue
		}
		out = append(out, ev)
	}
	return out, errs
}

func buildEvent(eventType string, unixNano uint64, attrs map[string]string) (audit.Event, error) {
	if err := validateAttrs(eventType, attrs); err != nil {
		return audit.Event{}, err
	}

	actorType := audit.ActorTypeUser
	if attrs[AttrRequesterType] == audit.ActorTypeAgent {
		actorType = audit.ActorTypeAgent
	}

	payload := map[string]any{
		"executor": attrs[AttrExecutor],
		"action":   attrs[AttrToolCallAct],
	}
	if v := attrs[AttrApprover]; v != "" {
		payload["approver"] = v
	}
	if v := attrs[AttrCredentialID]; v != "" {
		payload["credential_id"] = v
	}
	if v := attrs[AttrExternalURL]; v != "" {
		payload["external_object_url"] = v
	}

	return audit.Event{
		OccurredAt:  time.Unix(0, int64(unixNano)).UTC(),
		Source:      audit.SourceRuntime,
		EventType:   eventType,
		ActorType:   actorType,
		ActorID:     attrs[AttrRequester],
		TargetType:  "tool_call",
		TargetID:    attrs[AttrToolCallID],
		WorkspaceID: attrs[AttrWorkspaceID],
		Payload:     payload,
	}, nil
}

// validateAttrs enforces the Parsar schema. Returns the FIRST
// missing key as a descriptive error.
func validateAttrs(eventType string, attrs map[string]string) error {
	required := []string{
		AttrWorkspaceID,
		AttrRequester,
		AttrExecutor,
		AttrToolCallID,
		AttrToolCallAct,
	}
	for _, key := range required {
		if attrs[key] == "" {
			return fmt.Errorf("missing required attribute %s", key)
		}
	}
	if _, ok := eventTypesRequiringApprover[eventType]; ok {
		if attrs[AttrApprover] == "" {
			return fmt.Errorf("event %s requires %s", eventType, AttrApprover)
		}
	}
	if _, ok := eventTypesRequiringCredential[eventType]; ok {
		if attrs[AttrCredentialID] == "" {
			return fmt.Errorf("event %s requires %s", eventType, AttrCredentialID)
		}
	}
	return nil
}

// flattenAttrs converts an OTLP attribute list into a flat key→string
// map. Non-string values are stringified because audit.Event payloads
// are JSON-encoded downstream anyway.
func flattenAttrs(kvs []*commonpb.KeyValue) map[string]string {
	out := make(map[string]string, len(kvs))
	for _, kv := range kvs {
		out[kv.GetKey()] = anyToString(kv.GetValue())
	}
	return out
}

// mergeAttrs returns a new map: base entries first, then overridden by
// overlay. Neither input is mutated.
func mergeAttrs(base, overlay map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(overlay))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overlay {
		out[k] = v
	}
	return out
}

// anyToString stringifies an OTLP AnyValue. nil and unknown variants
// become "" so downstream `attrs[key] == ""` checks behave uniformly.
func anyToString(v *commonpb.AnyValue) string {
	if v == nil {
		return ""
	}
	switch x := v.Value.(type) {
	case *commonpb.AnyValue_StringValue:
		return x.StringValue
	case *commonpb.AnyValue_BoolValue:
		return fmt.Sprintf("%t", x.BoolValue)
	case *commonpb.AnyValue_IntValue:
		return fmt.Sprintf("%d", x.IntValue)
	case *commonpb.AnyValue_DoubleValue:
		return fmt.Sprintf("%g", x.DoubleValue)
	default:
		return ""
	}
}

func hasPrefix(s, prefix string) bool {
	if len(prefix) > len(s) {
		return false
	}
	return s[:len(prefix)] == prefix
}
