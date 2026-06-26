package feishu

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
)

var (
	ErrWebhookTokenMismatch = errors.New("feishu webhook verification token mismatch")
	ErrWebhookDecryptFailed = errors.New("feishu webhook decrypt failed")
)

type webhookEnvelope struct {
	// Encrypt holds the AES-256-CBC base64 payload when Event
	// Encryption is enabled at the Feishu open platform.
	Encrypt string `json:"encrypt"`

	// Token + Type + Challenge: the v1 / URL-verification fields.
	// URL challenge always carries these at the top level regardless
	// of event schema version.
	Token     string `json:"token"`
	Type      string `json:"type"`
	Challenge string `json:"challenge"`

	// Schema + Header: the v2.0 event envelope. Production
	// `im.message.receive_v1` etc. callbacks carry the verification
	// token in header.token, NOT top-level token (which is only
	// populated for legacy v1 events + URL challenge). resolveToken
	// accepts either.
	Schema string          `json:"schema"`
	Header webhookHeaderV2 `json:"header"`
}

type webhookHeaderV2 struct {
	Token string `json:"token"`
}

// resolveToken prefers header.token (real v2 events) over top-level
// token (URL challenges and legacy v1).
func (e webhookEnvelope) resolveToken() string {
	if t := strings.TrimSpace(e.Header.Token); t != "" {
		return t
	}
	return strings.TrimSpace(e.Token)
}

// VerifyAndDecodeEvent authenticates and unwraps a Feishu event callback.
//
// Supported Feishu event v2 security modes:
//   - Verification Token: every decoded event must carry token == verifyToken.
//   - Event Encryption: when body is {"encrypt":"..."}, decrypt using
//     AES-256-CBC with sha256(encryptKey) as the key and key[:16] as IV.
//   - URL Challenge: after token verification, returns isChallenge=true and
//     the challenge string so the HTTP handler can echo it back.
func VerifyAndDecodeEvent(body []byte, verifyToken, encryptKey string) (decodedJSON []byte, isChallenge bool, challenge string, err error) {
	verifyToken = strings.TrimSpace(verifyToken)
	if verifyToken == "" {
		return nil, false, "", ErrWebhookTokenMismatch
	}

	decoded := bytes.TrimSpace(body)
	var envelope webhookEnvelope
	if err := json.Unmarshal(decoded, &envelope); err != nil {
		return nil, false, "", err
	}
	if strings.TrimSpace(envelope.Encrypt) != "" {
		plain, err := decryptWebhookPayload(envelope.Encrypt, encryptKey)
		if err != nil {
			return nil, false, "", err
		}
		decoded = bytes.TrimSpace(plain)
		if err := json.Unmarshal(decoded, &envelope); err != nil {
			return nil, false, "", err
		}
	}

	if envelope.resolveToken() != verifyToken {
		return nil, false, "", ErrWebhookTokenMismatch
	}
	if envelope.Type == "url_verification" {
		return decoded, true, envelope.Challenge, nil
	}
	return decoded, false, "", nil
}

func decryptWebhookPayload(encrypted string, encryptKey string) ([]byte, error) {
	encryptKey = strings.TrimSpace(encryptKey)
	if encryptKey == "" {
		return nil, ErrWebhookDecryptFailed
	}
	ciphertext, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil {
		return nil, ErrWebhookDecryptFailed
	}
	key := sha256.Sum256([]byte(encryptKey))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, ErrWebhookDecryptFailed
	}
	if len(ciphertext) == 0 || len(ciphertext)%block.BlockSize() != 0 {
		return nil, ErrWebhookDecryptFailed
	}
	plain := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, key[:block.BlockSize()]).CryptBlocks(plain, ciphertext)
	plain, err = pkcs7Unpad(plain, block.BlockSize())
	if err != nil {
		return nil, ErrWebhookDecryptFailed
	}
	return plain, nil
}

func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 || len(data)%blockSize != 0 {
		return nil, ErrWebhookDecryptFailed
	}
	pad := int(data[len(data)-1])
	if pad == 0 || pad > blockSize || pad > len(data) {
		return nil, ErrWebhookDecryptFailed
	}
	for _, b := range data[len(data)-pad:] {
		if int(b) != pad {
			return nil, ErrWebhookDecryptFailed
		}
	}
	return data[:len(data)-pad], nil
}
