// Package discord — inbound request verification (PR #5c).
//
// The Discord Gateway WebSocket is authenticated once at the handshake (the bot
// token rides the IDENTIFY op), so individual gateway events carry no per-event
// signature — unlike the HTTP Interactions Endpoint, which signs each request
// with an Ed25519 header. This gateway consumes the WebSocket, so Verify is a
// pass-through: it returns the body unchanged with no challenge, mirroring the
// Slack Socket Mode path (slack/verify.go) where the socket is likewise
// pre-authenticated.
//
// The signature stays the neutral request-auth seam the manager calls before
// Normalize, so a future HTTP Interactions endpoint can grow an Ed25519 branch
// here without changing callers.
package discord

import "net/http"

// Verify authenticates an inbound Discord payload. On the Gateway WebSocket the
// connection is authenticated at handshake, so per-event verification is a
// pass-through: the verified bytes are the body as-is and there is no challenge
// to echo (Discord's Gateway has no url_verification handshake — that belongs to
// the HTTP Interactions endpoint, which this gateway does not use).
func (c *Channel) Verify(_ *http.Request, body []byte) (verified []byte, challenge string, err error) {
	return body, "", nil
}
