// Package otlp embeds a minimal OTLP/HTTP receiver that turns
// Parsar-tagged OpenTelemetry signals into audit.Event values.
//
// The receiver is intentionally NOT a general-purpose OpenTelemetry
// collector: it only honors attributes prefixed with `parsar.` and
// rejects payloads missing required schema fields. Operators who want
// to fan out to Tempo / Honeycomb / Datadog should point producers at
// both this receiver AND their collector.
package otlp
