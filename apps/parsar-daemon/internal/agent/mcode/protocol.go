// Package mcode drives MiniMax Code through its native ACP stdio interface.
package mcode

import "encoding/json"

type rpcFrame struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type sessionResult struct {
	SessionID     string         `json:"sessionId"`
	ConfigOptions []configOption `json:"configOptions"`
}

type configOption struct {
	ID      string `json:"id"`
	Options []struct {
		Value string `json:"value"`
	} `json:"options"`
}

type toolUpdate struct {
	ID        string         `json:"toolCallId"`
	Name      string         `json:"name"`
	Title     string         `json:"title"`
	Status    string         `json:"status"`
	RawInput  map[string]any `json:"rawInput"`
	RawOutput any            `json:"rawOutput"`
}

type sessionUpdate struct {
	SessionID string `json:"sessionId"`
	Update    struct {
		Kind    string `json:"sessionUpdate"`
		Content struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		toolUpdate
	} `json:"update"`
}

type permissionRequest struct {
	SessionID string     `json:"sessionId"`
	ToolCall  toolUpdate `json:"toolCall"`
	Options   []struct {
		ID   string `json:"optionId"`
		Kind string `json:"kind"`
	} `json:"options"`
}

type pendingPermission struct {
	RPCID json.RawMessage
	Allow string
	Deny  string
}
