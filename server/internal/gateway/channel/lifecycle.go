package channel

// Lifecycle is an optional sub-interface for connection lifecycle and health
// signals (relevant to WS-based inbound such as Slack Socket Mode or Discord
// Gateway). The driver probes with a type assertion; HTTP-webhook channels
// may omit it.
type Lifecycle interface {
	OnConnect() error
	OnDisconnect()
	// OnFatalError is called on an unrecoverable error. The driver stops the
	// channel and writes an audit record; retryable hints whether a later
	// reconnect attempt is worthwhile.
	OnFatalError(code string, err error, retryable bool)
}
