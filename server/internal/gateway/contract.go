package gateway

type InboundMessage struct {
	Gateway         string          `json:"gateway"`
	Message         MessageRef      `json:"message"`
	Actor           ActorRef        `json:"actor"`
	ConversationRef ConversationRef `json:"conversation_ref"`
	Metadata        map[string]any  `json:"metadata,omitempty"`
}

type MessageRef struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type ActorRef struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

type ConversationRef struct {
	ID       string `json:"id"`
	Title    string `json:"title,omitempty"`
	ThreadID string `json:"thread_id,omitempty"`
}

type Delivery struct {
	MessageID        string         `json:"message_id"`
	Gateway          string         `json:"gateway"`
	ExternalChatID   string         `json:"external_chat_id"`
	ExternalThreadID string         `json:"external_thread_id,omitempty"`
	Text             string         `json:"text"`
	DeliveryKey      string         `json:"delivery_key"`
	Metadata         map[string]any `json:"metadata,omitempty"`
}
