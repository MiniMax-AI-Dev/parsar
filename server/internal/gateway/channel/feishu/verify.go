package feishu

import (
	"net/http"

	authfeishu "github.com/MiniMax-AI-Dev/parsar/server/internal/auth/feishu"
)

// Verify delegates to the existing auth/feishu webhook verifier so the
// authentication + URL-challenge + AES-decrypt behavior is byte-for-byte the
// production behavior. The *http.Request is unused for Feishu (the
// verification token rides inside the body); it is part of the neutral
// Channel contract because Slack/Discord verify via request headers.
func (c *Channel) Verify(_ *http.Request, body []byte) (verified []byte, challenge string, err error) {
	decoded, _, challenge, err := authfeishu.VerifyAndDecodeEvent(body, c.verifyToken, c.encryptKey)
	if err != nil {
		return nil, "", err
	}
	return decoded, challenge, nil
}
