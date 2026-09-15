package main

import (
	"encoding/base64"
	"errors"
	"os"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/credentialcrypto"
)

func credentialCipher() (*credentialcrypto.Cipher, error) {
	path := os.Getenv("AGENTS_API_CREDENTIAL_KEY_FILE")
	if path == "" {
		return nil, nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("cannot read AGENTS_API_CREDENTIAL_KEY_FILE")
	}
	key, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(string(content)))
	if err != nil || len(key) != 32 {
		return nil, errors.New("AGENTS_API_CREDENTIAL_KEY_FILE must contain a base64-encoded random 32-byte key")
	}
	return credentialcrypto.New(key)
}
