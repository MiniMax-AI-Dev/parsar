package slack

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// slackSignature reproduces Slack's v0 request signing scheme:
// hex(HMAC-SHA256(secret, "v0:<ts>:<body>")).
func slackSignature(secret, ts string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "v0:%s:%s", ts, body)
	return "v0=" + hex.EncodeToString(mac.Sum(nil))
}

const (
	urlVerificationBody = `{"type":"url_verification","challenge":"abc123","token":"t"}`
	eventCallbackBody   = `{"type":"event_callback","team_id":"T1","api_app_id":"A123","event":{"type":"app_mention","user":"U1","text":"hi","ts":"1.2","channel":"C1"}}`
)

func TestVerify_URLVerificationReturnsChallenge(t *testing.T) {
	c := newTestChannel() // no signing secret
	verified, challenge, err := c.Verify(nil, []byte(urlVerificationBody))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if challenge != "abc123" {
		t.Errorf("challenge = %q, want abc123", challenge)
	}
	if verified != nil {
		t.Errorf("url_verification must yield no verified body, got %q", verified)
	}
}

func TestVerify_EventCallbackReturnsBodyUnchanged(t *testing.T) {
	c := newTestChannel()
	verified, challenge, err := c.Verify(nil, []byte(eventCallbackBody))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if challenge != "" {
		t.Errorf("event_callback must not carry a challenge, got %q", challenge)
	}
	if string(verified) != eventCallbackBody {
		t.Errorf("verified body = %q, want it unchanged", verified)
	}
}

func TestVerify_ValidSignaturePasses(t *testing.T) {
	const secret = "shhh"
	c := New(Config{AppID: "A123", BotToken: "xoxb-test", SigningSecret: secret})
	body := []byte(eventCallbackBody)
	ts := strconv.FormatInt(time.Now().Unix(), 10)

	r := httptest.NewRequest("POST", "/", nil)
	r.Header.Set("X-Slack-Request-Timestamp", ts)
	r.Header.Set("X-Slack-Signature", slackSignature(secret, ts, body))

	verified, _, err := c.Verify(r, body)
	if err != nil {
		t.Fatalf("Verify with a valid signature must pass: %v", err)
	}
	if string(verified) != eventCallbackBody {
		t.Errorf("verified = %q, want the body", verified)
	}
}

func TestVerify_BadSignatureRejected(t *testing.T) {
	c := New(Config{AppID: "A123", BotToken: "xoxb-test", SigningSecret: "shhh"})
	body := []byte(eventCallbackBody)
	ts := strconv.FormatInt(time.Now().Unix(), 10)

	r := httptest.NewRequest("POST", "/", nil)
	r.Header.Set("X-Slack-Request-Timestamp", ts)
	// Signature computed under the WRONG secret.
	r.Header.Set("X-Slack-Signature", slackSignature("wrong", ts, body))

	if _, _, err := c.Verify(r, body); err == nil {
		t.Fatal("Verify must reject a bad signature")
	}
}

func TestVerify_SkipsHMACWhenNoSecret(t *testing.T) {
	// Socket Mode path: no signing secret, request without signature headers
	// still verifies (the websocket was authenticated at handshake).
	c := newTestChannel()
	r := httptest.NewRequest("POST", "/", nil)
	verified, _, err := c.Verify(r, []byte(eventCallbackBody))
	if err != nil {
		t.Fatalf("Verify without a secret must skip the HMAC check: %v", err)
	}
	if string(verified) != eventCallbackBody {
		t.Errorf("verified = %q, want the body", verified)
	}
}

func TestVerify_MissingSignatureHeadersRejected(t *testing.T) {
	// Signing secret configured but the request omits the signature headers:
	// NewSecretsVerifier must reject it rather than waving the request through.
	c := New(Config{AppID: "A123", BotToken: "xoxb-test", SigningSecret: "shhh"})
	r := httptest.NewRequest("POST", "/", nil)
	if _, _, err := c.Verify(r, []byte(eventCallbackBody)); err == nil {
		t.Fatal("Verify must reject a request missing the signature headers")
	}
}

func TestVerify_MalformedBodyErrors(t *testing.T) {
	c := newTestChannel()
	if _, _, err := c.Verify(nil, []byte("not json")); err == nil {
		t.Fatal("Verify must error on a non-JSON body")
	}
}
