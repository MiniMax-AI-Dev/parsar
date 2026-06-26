package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	EnvelopeVersion = "v1"
	EnvelopeAlg     = "AES-256-GCM"
)

var ErrMasterKeyRequired = errors.New("PARSAR_MASTER_KEY is required for secrets")

type Envelope struct {
	Version    string `json:"version"`
	Alg        string `json:"alg"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
	KeyVersion string `json:"key_version"`
}

type Service struct {
	key        []byte
	keyVersion string
}

func New(masterKey string) (*Service, error) {
	masterKey = strings.TrimSpace(masterKey)
	if masterKey == "" {
		return nil, ErrMasterKeyRequired
	}
	key, err := normalizeKey(masterKey)
	if err != nil {
		return nil, err
	}
	return &Service{key: key, keyVersion: EnvelopeVersion}, nil
}

func (s *Service) Encrypt(payload map[string]any) ([]byte, error) {
	if payload == nil {
		payload = map[string]any{}
	}
	plain, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	ciphertext := aead.Seal(nil, nonce, plain, nil)
	envelope := Envelope{
		Version:    EnvelopeVersion,
		Alg:        EnvelopeAlg,
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
		KeyVersion: s.keyVersion,
	}
	return json.Marshal(envelope)
}

func (s *Service) Decrypt(envelopeJSON []byte) (map[string]any, error) {
	var envelope Envelope
	if err := json.Unmarshal(envelopeJSON, &envelope); err != nil {
		return nil, err
	}
	if envelope.Version != EnvelopeVersion || envelope.Alg != EnvelopeAlg {
		return nil, fmt.Errorf("unsupported secret envelope %s/%s", envelope.Version, envelope.Alg)
	}
	nonce, err := base64.StdEncoding.DecodeString(envelope.Nonce)
	if err != nil {
		return nil, err
	}
	ciphertext, err := base64.StdEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plain, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(plain, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func normalizeKey(value string) ([]byte, error) {
	if strings.HasPrefix(value, "base64:") {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, "base64:"))
		if err != nil {
			return nil, err
		}
		if len(decoded) == 32 {
			return decoded, nil
		}
	}
	if len(value) == 32 {
		return []byte(value), nil
	}
	sum := sha256.Sum256([]byte(value))
	return sum[:], nil
}

func MaskPayload(payload map[string]any) string {
	for _, key := range []string{"api_key", "token", "access_token", "app_secret", "verification_token"} {
		if raw, ok := payload[key].(string); ok && strings.TrimSpace(raw) != "" {
			return mask(raw)
		}
	}
	return "configured"
}

func mask(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 8 {
		return "****"
	}
	return value[:min(6, len(value))] + "..." + value[len(value)-4:]
}
