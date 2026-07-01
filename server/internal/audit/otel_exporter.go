package audit

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"
)

// OTelExporterSink translates audit.Event into an OTLP/HTTP Log record
// and POSTs it to a customer-configured collector. Optional fan-out
// target enabled via audit.otlp.fanout_endpoint.
//
// Logs (not Trace spans): audit events are single-shot (no duration);
// the OTel data model maps these to LogRecord. Customer log UIs
// (Loki / OTel logs) get the right shape; trace UIs (Tempo / Jaeger)
// would not.
//
// Hand-rolled HTTP POST with protobuf body avoids pulling the full
// OTel SDK just to ship a single export call. Per-Write timeout is
// bounded so a stuck collector never blocks the Ingester worker.
//
// A failed fan-out MUST NOT cause the authoritative PostgresSink
// write to be reported as a failure (MultiSink logs + swallows).
type OTelExporterSink struct {
	endpoint string
	client   *http.Client
}

type OTelExporterOptions struct {
	// Endpoint is the collector's BASE url (e.g. https://otel:4318);
	// `/v1/logs` is appended automatically. Required.
	Endpoint string
	// HTTPClient overrides the default 5s-timeout client.
	HTTPClient *http.Client
}

// NewOTelExporterSink validates the endpoint and returns an unstarted sink.
// Empty endpoint is an error so a misconfigured process fails startup
// instead of silently dropping every fan-out write.
//
// A trailing `/v1/logs` is stripped before re-appending — otherwise the
// common operator mistake of pasting the full URL produces
// `/v1/logs/v1/logs` and every export fails 404 in a way that is hard
// to diagnose from the WARN log alone.
func NewOTelExporterSink(opts OTelExporterOptions) (*OTelExporterSink, error) {
	endpoint := strings.TrimSpace(opts.Endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("audit.NewOTelExporterSink: endpoint is required")
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	endpoint = strings.TrimRight(endpoint, "/")
	const logsPath = "/v1/logs"
	if strings.HasSuffix(endpoint, logsPath) {
		endpoint = strings.TrimSuffix(endpoint, logsPath)
	}
	return &OTelExporterSink{
		endpoint: endpoint + logsPath,
		client:   client,
	}, nil
}

// Write converts ev into an OTLP ExportLogsServiceRequest and POSTs it.
func (s *OTelExporterSink) Write(ctx context.Context, ev Event) error {
	body, err := encodeEventAsOTLPLog(ev)
	if err != nil {
		return fmt.Errorf("audit.OTelExporterSink: encode: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint,
		bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("audit.OTelExporterSink: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("audit.OTelExporterSink: POST %s: %w", s.endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("audit.OTelExporterSink: %s returned %d", s.endpoint, resp.StatusCode)
	}
	return nil
}

// encodeEventAsOTLPLog builds the OTLP/HTTP protobuf payload for a
// single Event. Attribute names use the `parsar.*` namespace.
func encodeEventAsOTLPLog(ev Event) ([]byte, error) {
	ts := ev.OccurredAt
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	attrs := []*commonpb.KeyValue{
		strAttr("parsar.audit.source", ev.Source),
		strAttr("parsar.audit.event_type", ev.EventType),
		strAttr("parsar.audit.actor_type", ev.ActorType),
	}
	if ev.ActorID != "" {
		attrs = append(attrs, strAttr("parsar.audit.actor_id", ev.ActorID))
	}
	if ev.TargetType != "" {
		attrs = append(attrs, strAttr("parsar.audit.target_type", ev.TargetType))
	}
	if ev.TargetID != "" {
		attrs = append(attrs, strAttr("parsar.audit.target_id", ev.TargetID))
	}
	if ev.WorkspaceID != "" {
		attrs = append(attrs, strAttr("parsar.workspace_id", ev.WorkspaceID))
	}
	for k, v := range ev.Payload {
		attrs = append(attrs, strAttr("parsar.audit.payload."+k,
			fmt.Sprintf("%v", v)))
	}

	record := &logspb.LogRecord{
		TimeUnixNano:         uint64(ts.UnixNano()),
		ObservedTimeUnixNano: uint64(time.Now().UnixNano()),
		SeverityNumber:       logspb.SeverityNumber_SEVERITY_NUMBER_INFO,
		SeverityText:         "INFO",
		Body: &commonpb.AnyValue{
			Value: &commonpb.AnyValue_StringValue{
				StringValue: ev.Source + "." + ev.EventType,
			},
		},
		Attributes: attrs,
	}

	req := &collogspb.ExportLogsServiceRequest{
		ResourceLogs: []*logspb.ResourceLogs{{
			Resource: &resourcepb.Resource{
				Attributes: []*commonpb.KeyValue{
					strAttr("service.name", "parsar-server"),
				},
			},
			ScopeLogs: []*logspb.ScopeLogs{{
				LogRecords: []*logspb.LogRecord{record},
			}},
		}},
	}
	return proto.Marshal(req)
}

func strAttr(key, value string) *commonpb.KeyValue {
	return &commonpb.KeyValue{
		Key: key,
		Value: &commonpb.AnyValue{
			Value: &commonpb.AnyValue_StringValue{StringValue: value},
		},
	}
}

var _ Sink = (*OTelExporterSink)(nil)
