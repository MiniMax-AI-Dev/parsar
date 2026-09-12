package proto

import "encoding/json"

// ToolObservation is an engine-neutral execution snapshot, not a public API Item.
type ToolObservation struct {
	Kind       string                   `json:"kind"`
	Status     string                   `json:"status"`
	Name       string                   `json:"name,omitempty"`
	Command    string                   `json:"command,omitempty"`
	Cwd        *string                  `json:"cwd,omitempty"`
	ExitCode   *int64                   `json:"exit_code,omitempty"`
	DurationMS *int64                   `json:"duration_ms,omitempty"`
	Server     string                   `json:"server,omitempty"`
	Arguments  json.RawMessage          `json:"arguments,omitempty"`
	Output     json.RawMessage          `json:"output,omitempty"`
	Error      json.RawMessage          `json:"error,omitempty"`
	Content    *[]FunctionResultContent `json:"content,omitempty"`
	Action     *ToolWebSearchAction     `json:"action,omitempty"`
}

type ToolWebSearchAction struct {
	Type    string   `json:"type"`
	Query   *string  `json:"query,omitempty"`
	Queries []string `json:"queries,omitempty"`
	URL     *string  `json:"url,omitempty"`
	Pattern *string  `json:"pattern,omitempty"`
}
