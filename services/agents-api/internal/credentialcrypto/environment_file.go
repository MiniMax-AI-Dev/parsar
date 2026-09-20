package credentialcrypto

import (
	"encoding/json"
	"unicode/utf8"
)

// EnvironmentFileBinding keeps confidential bytes scoped to one resource and file.
type EnvironmentFileBinding struct {
	TenantID string `json:"tenant_id"`
	Resource string `json:"resource"`
	OwnerID  string `json:"owner_id"`
	FileID   string `json:"file_id"`
}

func (c *Cipher) SealEnvironmentFile(plaintext []byte, binding EnvironmentFileBinding) ([]byte, error) {
	aad, err := environmentFileData(binding)
	if err != nil {
		return nil, err
	}
	return c.seal(plaintext, aad)
}

func (c *Cipher) OpenEnvironmentFile(ciphertext []byte, binding EnvironmentFileBinding) ([]byte, error) {
	aad, err := environmentFileData(binding)
	if err != nil {
		return nil, err
	}
	return c.open(ciphertext, aad)
}

func environmentFileData(binding EnvironmentFileBinding) ([]byte, error) {
	if binding.Resource != "environment_template" && binding.Resource != "session" {
		return nil, errInvalidBinding
	}
	for _, value := range []string{binding.TenantID, binding.OwnerID, binding.FileID} {
		if value == "" || !utf8.ValidString(value) {
			return nil, errInvalidBinding
		}
	}
	return json.Marshal(struct {
		Domain  string                 `json:"domain"`
		Version byte                   `json:"version"`
		Binding EnvironmentFileBinding `json:"binding"`
	}{"parsar.agents-api.environment-file", formatVersion, binding})
}
