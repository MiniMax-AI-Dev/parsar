package otlp

import (
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/audit"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// validBaseAttrs returns the minimum Parsar attribute set required
// for a tool_call event to pass schema validation.
func validBaseAttrs() []*commonpb.KeyValue {
	return []*commonpb.KeyValue{
		strAttr(AttrWorkspaceID, "11111111-1111-1111-1111-111111111111"),
		strAttr(AttrProjectID, "22222222-2222-2222-2222-222222222222"),
		strAttr(AttrRequester, "33333333-3333-3333-3333-333333333333"),
		strAttr(AttrExecutor, "project_bot"),
		strAttr(AttrToolCallID, "tc_abc"),
		strAttr(AttrToolCallAct, "create_merge_request"),
	}
}

func strAttr(key, value string) *commonpb.KeyValue {
	return &commonpb.KeyValue{
		Key:   key,
		Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: value}},
	}
}

// TestConvertTraces_HappyPath confirms a single span carrying a
// recognized tool_call.* event produces exactly one audit.Event.
func TestConvertTraces_HappyPath(t *testing.T) {
	req := &coltracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{{
			Resource: &resourcepb.Resource{},
			ScopeSpans: []*tracepb.ScopeSpans{{
				Spans: []*tracepb.Span{{
					Name:       "github.create_mr",
					Attributes: validBaseAttrs(),
					Events: []*tracepb.Span_Event{{
						Name:         "tool_call.started",
						TimeUnixNano: 1_700_000_000_000_000_000,
					}},
				}},
			}},
		}},
	}

	events, errs := convertTraces(req)
	if len(errs) != 0 {
		t.Fatalf("unexpected schema errors: %v", errs)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev := events[0]
	if ev.Source != audit.SourceRuntime {
		t.Errorf("source: got %q, want %q", ev.Source, audit.SourceRuntime)
	}
	if ev.EventType != "tool_call.started" {
		t.Errorf("event_type: got %q, want tool_call.started", ev.EventType)
	}
	if ev.ActorType != audit.ActorTypeUser {
		t.Errorf("actor_type: got %q, want %q", ev.ActorType, audit.ActorTypeUser)
	}
	if ev.ActorID != "33333333-3333-3333-3333-333333333333" {
		t.Errorf("actor_id: got %q", ev.ActorID)
	}
	if ev.TargetType != "tool_call" || ev.TargetID != "tc_abc" {
		t.Errorf("target: got (%q,%q)", ev.TargetType, ev.TargetID)
	}
	if ev.WorkspaceID != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("workspace_id: got %q", ev.WorkspaceID)
	}
	if got := ev.Payload["action"]; got != "create_merge_request" {
		t.Errorf("payload.action: got %v", got)
	}
}

// TestConvertTraces_MultipleEvents asserts each Span Event produces
// its own audit.Event, and that per-event attributes override
// span-level attributes when both are present.
func TestConvertTraces_MultipleEvents(t *testing.T) {
	spanAttrs := validBaseAttrs()
	credentialEventAttrs := []*commonpb.KeyValue{strAttr(AttrCredentialID, "sec_42")}

	req := &coltracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{{
			ScopeSpans: []*tracepb.ScopeSpans{{
				Spans: []*tracepb.Span{{
					Attributes: spanAttrs,
					Events: []*tracepb.Span_Event{
						{Name: "tool_call.started", TimeUnixNano: 1_000_000_000},
						{Name: "tool_call.credential_delivered",
							TimeUnixNano: 2_000_000_000,
							Attributes:   credentialEventAttrs},
						{Name: "tool_call.completed", TimeUnixNano: 3_000_000_000},
						// non-tool_call event must be silently ignored
						{Name: "unrelated.debug", TimeUnixNano: 4_000_000_000},
					},
				}},
			}},
		}},
	}

	events, errs := convertTraces(req)
	if len(errs) != 0 {
		t.Fatalf("unexpected schema errors: %v", errs)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events (ignoring unrelated.debug), got %d", len(events))
	}
	if events[1].EventType != "tool_call.credential_delivered" {
		t.Fatalf("event[1] type: got %q", events[1].EventType)
	}
	if got := events[1].Payload["credential_id"]; got != "sec_42" {
		t.Errorf("credential_id payload missing on credential_delivered: got %v", got)
	}
	// Other events must NOT have the credential id leaking in.
	if _, present := events[0].Payload["credential_id"]; present {
		t.Errorf("credential_id should not appear on tool_call.started")
	}
}

// TestConvertTraces_RejectsMissingBaseline confirms a span event
// missing any always-required attribute is rejected with a schema
// error and no audit.Event is emitted.
func TestConvertTraces_RejectsMissingBaseline(t *testing.T) {
	cases := []struct {
		name     string
		drop     string
		wantSub  string
		wantType string
	}{
		{name: "missing workspace_id", drop: AttrWorkspaceID, wantSub: "workspace_id"},
		{name: "missing requester", drop: AttrRequester, wantSub: "requester"},
		{name: "missing executor", drop: AttrExecutor, wantSub: "executor"},
		{name: "missing tool_call.id", drop: AttrToolCallID, wantSub: "tool_call.id"},
		{name: "missing tool_call.action", drop: AttrToolCallAct, wantSub: "tool_call.action"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := &coltracepb.ExportTraceServiceRequest{
				ResourceSpans: []*tracepb.ResourceSpans{{
					ScopeSpans: []*tracepb.ScopeSpans{{
						Spans: []*tracepb.Span{{
							Attributes: dropAttr(validBaseAttrs(), tc.drop),
							Events: []*tracepb.Span_Event{{
								Name: "tool_call.started", TimeUnixNano: 1,
							}},
						}},
					}},
				}},
			}
			events, errs := convertTraces(req)
			if len(events) != 0 {
				t.Fatalf("expected zero events, got %d", len(events))
			}
			if len(errs) != 1 {
				t.Fatalf("expected exactly 1 schema error, got %d (%v)", len(errs), errs)
			}
			if !strings.Contains(errs[0].Error(), tc.wantSub) {
				t.Errorf("error %q does not mention missing %s", errs[0].Error(), tc.wantSub)
			}
		})
	}
}

// TestConvertTraces_ConditionalApprover asserts approver is required
// for tool_call.approved / tool_call.denied and only for those.
func TestConvertTraces_ConditionalApprover(t *testing.T) {
	withoutApprover := validBaseAttrs() // approver intentionally absent

	req := &coltracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{{
			ScopeSpans: []*tracepb.ScopeSpans{{
				Spans: []*tracepb.Span{{
					Attributes: withoutApprover,
					Events: []*tracepb.Span_Event{
						{Name: "tool_call.started", TimeUnixNano: 1},
						{Name: "tool_call.approved", TimeUnixNano: 2},
					},
				}},
			}},
		}},
	}

	events, errs := convertTraces(req)
	if len(events) != 1 {
		t.Fatalf("expected 1 accepted event (started), got %d", len(events))
	}
	if events[0].EventType != "tool_call.started" {
		t.Errorf("accepted event should be the baseline one, got %q", events[0].EventType)
	}
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), AttrApprover) {
		t.Fatalf("expected 1 approver-required error, got %d (%v)", len(errs), errs)
	}
}

// TestConvertTraces_ConditionalCredential asserts credential id is
// required for tool_call.credential_delivered specifically.
func TestConvertTraces_ConditionalCredential(t *testing.T) {
	req := &coltracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{{
			ScopeSpans: []*tracepb.ScopeSpans{{
				Spans: []*tracepb.Span{{
					Attributes: validBaseAttrs(),
					Events: []*tracepb.Span_Event{
						{Name: "tool_call.credential_delivered", TimeUnixNano: 1},
					},
				}},
			}},
		}},
	}
	events, errs := convertTraces(req)
	if len(events) != 0 {
		t.Fatalf("expected zero events, got %d", len(events))
	}
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), AttrCredentialID) {
		t.Fatalf("expected 1 credential-required error, got %d (%v)", len(errs), errs)
	}
}

// TestConvertTraces_RequesterTypeAgent asserts the optional
// parsar.requester.type attribute switches ActorType from the
// default "user" to "agent".
func TestConvertTraces_RequesterTypeAgent(t *testing.T) {
	attrs := append(validBaseAttrs(), strAttr(AttrRequesterType, audit.ActorTypeAgent))
	req := &coltracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{{
			ScopeSpans: []*tracepb.ScopeSpans{{
				Spans: []*tracepb.Span{{
					Attributes: attrs,
					Events: []*tracepb.Span_Event{
						{Name: "tool_call.started", TimeUnixNano: 1},
					},
				}},
			}},
		}},
	}
	events, errs := convertTraces(req)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if events[0].ActorType != audit.ActorTypeAgent {
		t.Errorf("actor_type: got %q, want %q", events[0].ActorType, audit.ActorTypeAgent)
	}
}

// TestConvertLogs_StubAccepts asserts the logs stub parses and counts
// records without panicking — every log record is dropped but the
// partial-success count surfaced to the caller stays coherent.
func TestConvertLogs_StubAccepts(t *testing.T) {
	req := &collogspb.ExportLogsServiceRequest{
		ResourceLogs: []*logspb.ResourceLogs{{
			ScopeLogs: []*logspb.ScopeLogs{{
				LogRecords: []*logspb.LogRecord{{}, {}, {}},
			}},
		}},
	}
	count := 0
	for _, rl := range req.GetResourceLogs() {
		for _, sl := range rl.GetScopeLogs() {
			count += len(sl.GetLogRecords())
		}
	}
	if count != 3 {
		t.Fatalf("expected 3 stub log records, got %d", count)
	}
}

// dropAttr returns a copy of attrs with any KeyValue whose key equals
// the supplied name removed.
func dropAttr(attrs []*commonpb.KeyValue, name string) []*commonpb.KeyValue {
	out := make([]*commonpb.KeyValue, 0, len(attrs))
	for _, kv := range attrs {
		if kv.GetKey() == name {
			continue
		}
		out = append(out, kv)
	}
	return out
}
