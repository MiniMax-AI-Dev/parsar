// Package slack — inbound request verification (PR #4c).
//
// Verify authenticates an Events API HTTP request and answers Slack's
// url_verification handshake. Slack signs each request with an HMAC over the
// raw body keyed by the app Signing Secret (X-Slack-Signature /
// X-Slack-Request-Timestamp); slack.NewSecretsVerifier reproduces that check.
//
// Slack's *production* entry point in this gateway is Socket Mode, whose
// websocket is authenticated once at handshake with the App-Level Token, so
// individual events carry no per-request signature. On that path signingSecret
// is empty and Verify skips the HMAC check, still answering the one-shot
// url_verification challenge. The HMAC branch keeps the neutral contract honest
// for a future HTTP Events endpoint and mirrors Feishu's verify.go, which is
// likewise the request-auth seam the manager calls before Normalize.
package slack

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

// Verify authenticates an inbound Slack request and resolves the
// url_verification handshake.
//
//   - When signingSecret is configured and r is non-nil, the request HMAC is
//     verified over body; a bad signature returns an error and no verified
//     bytes.
//   - A url_verification event returns its challenge string (and no verified
//     body) for the HTTP handler to echo back verbatim.
//   - Any other (event_callback) request returns body unchanged as the
//     verified payload Normalize consumes.
func (c *Channel) Verify(r *http.Request, body []byte) (verified []byte, challenge string, err error) {
	if c.signingSecret != "" && r != nil {
		sv, err := slack.NewSecretsVerifier(r.Header, c.signingSecret)
		if err != nil {
			return nil, "", fmt.Errorf("slack channel: secrets verifier: %w", err)
		}
		if _, err := sv.Write(body); err != nil {
			return nil, "", fmt.Errorf("slack channel: hash request body: %w", err)
		}
		if err := sv.Ensure(); err != nil {
			return nil, "", fmt.Errorf("slack channel: verify signature: %w", err)
		}
	}

	// Peek at the event type to answer the url_verification challenge without
	// pulling in the full slackevents parse (which the callback path uses).
	var probe struct {
		Type      string `json:"type"`
		Challenge string `json:"challenge"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return nil, "", fmt.Errorf("slack channel: decode event envelope: %w", err)
	}
	if probe.Type == slackevents.URLVerification {
		return nil, probe.Challenge, nil
	}
	return body, "", nil
}
