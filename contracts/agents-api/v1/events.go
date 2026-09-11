package v1

// SessionEvent contains the supported live event variants of the pinned protocol.
type SessionEvent struct {
	Type         string       `json:"type" binding:"required"`
	EventID      string       `json:"event_id" binding:"required"`
	SessionID    string       `json:"session_id" binding:"required"`
	TurnID       string       `json:"turn_id,omitempty"`
	Session      *Session     `json:"session,omitempty"`
	Turn         *Turn        `json:"turn,omitempty"`
	Item         *Item        `json:"item,omitempty"`
	ItemID       string       `json:"item_id,omitempty"`
	OutputIndex  *int32       `json:"output_index,omitempty"`
	ContentIndex *int         `json:"content_index,omitempty"`
	Part         *ItemContent `json:"part,omitempty"`
	Delta        *string      `json:"delta,omitempty"`
	Text         *string      `json:"text,omitempty"`
	Error        *StreamError `json:"error,omitempty"`
}

type StreamError struct {
	Code    string `json:"code"`
	Type    string `json:"type"`
	Message string `json:"message"`
}
