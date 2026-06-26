package feishu

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func TestVerifyAndDecodeEventTokenMatch(t *testing.T) {
	body := []byte(`{"token":"verify-token","event":{"message":{"message_id":"om_1"}}}`)
	decoded, isChallenge, challenge, err := VerifyAndDecodeEvent(body, "verify-token", "")
	if err != nil {
		t.Fatal(err)
	}
	if isChallenge || challenge != "" || !bytes.Equal(decoded, body) {
		t.Fatalf("unexpected result decoded=%s isChallenge=%v challenge=%q", decoded, isChallenge, challenge)
	}
}

func TestVerifyAndDecodeEventTokenMismatch(t *testing.T) {
	_, _, _, err := VerifyAndDecodeEvent([]byte(`{"token":"bad"}`), "verify-token", "")
	if !errors.Is(err, ErrWebhookTokenMismatch) {
		t.Fatalf("expected token mismatch, got %v", err)
	}
}

func TestVerifyAndDecodeEventEncryptedBody(t *testing.T) {
	plain := []byte(`{"token":"verify-token","event":{"message":{"message_id":"om_1"}}}`)
	body := []byte(`{"encrypt":"` + encryptWebhookPayloadForTest(t, plain, "encrypt-key") + `"}`)
	decoded, isChallenge, _, err := VerifyAndDecodeEvent(body, "verify-token", "encrypt-key")
	if err != nil {
		t.Fatal(err)
	}
	if isChallenge || string(decoded) != string(plain) {
		t.Fatalf("decoded=%s isChallenge=%v", decoded, isChallenge)
	}
}

func TestVerifyAndDecodeEventDecryptFail(t *testing.T) {
	body := []byte(`{"encrypt":"not-base64"}`)
	_, _, _, err := VerifyAndDecodeEvent(body, "verify-token", "encrypt-key")
	if !errors.Is(err, ErrWebhookDecryptFailed) {
		t.Fatalf("expected decrypt failed, got %v", err)
	}
}

func TestVerifyAndDecodeEventChallenge(t *testing.T) {
	decoded, isChallenge, challenge, err := VerifyAndDecodeEvent([]byte(`{"type":"url_verification","challenge":"challenge-code","token":"verify-token"}`), "verify-token", "")
	if err != nil {
		t.Fatal(err)
	}
	if !isChallenge || challenge != "challenge-code" || !strings.Contains(string(decoded), "url_verification") {
		t.Fatalf("decoded=%s isChallenge=%v challenge=%q", decoded, isChallenge, challenge)
	}
}

func TestVerifyAndDecodeEventChallengeBadToken(t *testing.T) {
	_, _, _, err := VerifyAndDecodeEvent([]byte(`{"type":"url_verification","challenge":"challenge-code","token":"bad"}`), "verify-token", "")
	if !errors.Is(err, ErrWebhookTokenMismatch) {
		t.Fatalf("expected token mismatch, got %v", err)
	}
}

// TestVerifyAndDecodeEventV2HeaderToken: real Feishu v2 envelopes
// carry the token at header.token; legacy code path that read
// top-level token only would 401 these in prod.
func TestVerifyAndDecodeEventV2HeaderToken(t *testing.T) {
	body := []byte(`{"schema":"2.0","header":{"token":"verify-token","event_type":"im.message.receive_v1"},"event":{"message":{"message_id":"om_v2"}}}`)
	decoded, isChallenge, _, err := VerifyAndDecodeEvent(body, "verify-token", "")
	if err != nil {
		t.Fatal(err)
	}
	if isChallenge || !bytes.Equal(decoded, body) {
		t.Fatalf("v2 envelope decode unexpected: decoded=%s isChallenge=%v", decoded, isChallenge)
	}
}

func TestVerifyAndDecodeEventV2HeaderTokenMismatch(t *testing.T) {
	body := []byte(`{"schema":"2.0","header":{"token":"attacker"},"event":{}}`)
	_, _, _, err := VerifyAndDecodeEvent(body, "verify-token", "")
	if !errors.Is(err, ErrWebhookTokenMismatch) {
		t.Fatalf("expected v2 header token mismatch, got %v", err)
	}
}

// TestVerifyAndDecodeEventV2HeaderTokenWithEncryption: the fully
// realistic prod shape — encrypted body wrapping a v2 envelope.
func TestVerifyAndDecodeEventV2HeaderTokenWithEncryption(t *testing.T) {
	plain := []byte(`{"schema":"2.0","header":{"token":"verify-token","event_type":"im.message.receive_v1"},"event":{"message":{"message_id":"om_v2_enc"}}}`)
	body := []byte(`{"encrypt":"` + encryptWebhookPayloadForTest(t, plain, "encrypt-key") + `"}`)
	decoded, isChallenge, _, err := VerifyAndDecodeEvent(body, "verify-token", "encrypt-key")
	if err != nil {
		t.Fatal(err)
	}
	if isChallenge || !bytes.Equal(decoded, plain) {
		t.Fatalf("encrypted v2 decode unexpected: decoded=%s isChallenge=%v", decoded, isChallenge)
	}
}

// TestVerifyAndDecodeEventV2HeaderTokenPrefersOverLegacy pins the
// priority: header.token wins over legacy top-level token.
func TestVerifyAndDecodeEventV2HeaderTokenPrefersOverLegacy(t *testing.T) {
	body := []byte(`{"schema":"2.0","token":"legacy-bad","header":{"token":"verify-token"},"event":{}}`)
	_, _, _, err := VerifyAndDecodeEvent(body, "verify-token", "")
	if err != nil {
		t.Fatalf("header.token should win over legacy token: %v", err)
	}
}

func encryptWebhookPayloadForTest(t *testing.T, plain []byte, encryptKey string) string {
	t.Helper()
	key := sha256.Sum256([]byte(encryptKey))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		t.Fatal(err)
	}
	padded := pkcs7PadForTest(plain, block.BlockSize())
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, key[:block.BlockSize()]).CryptBlocks(out, padded)
	return base64.StdEncoding.EncodeToString(out)
}

func pkcs7PadForTest(data []byte, blockSize int) []byte {
	pad := blockSize - len(data)%blockSize
	out := make([]byte, len(data)+pad)
	copy(out, data)
	for i := len(data); i < len(out); i++ {
		out[i] = byte(pad)
	}
	return out
}
