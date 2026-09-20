package credentialcrypto

import (
	"encoding/json"
	"unicode/utf8"
)

// EnvironmentSetupBinding separates confidential fields and immutable snapshots.
type EnvironmentSetupBinding struct {
	TenantID string `json:"tenant_id"`
	Resource string `json:"resource"`
	OwnerID  string `json:"owner_id"`
	Field    string `json:"field"`
}

func (c *Cipher) SealEnvironmentSetup(plaintext []byte, binding EnvironmentSetupBinding) ([]byte, error) {
	aad, err := environmentSetupData(binding)
	if err != nil {
		return nil, err
	}
	return c.seal(plaintext, aad)
}

func (c *Cipher) OpenEnvironmentSetup(ciphertext []byte, binding EnvironmentSetupBinding) ([]byte, error) {
	aad, err := environmentSetupData(binding)
	if err != nil {
		return nil, err
	}
	return c.open(ciphertext, aad)
}

func environmentSetupData(binding EnvironmentSetupBinding) ([]byte, error) {
	if (binding.Resource != "environment_template" && binding.Resource != "session") || (binding.Field != "env" && binding.Field != "setup_commands" && binding.Field != "initialization") {
		return nil, errInvalidBinding
	}
	for _, value := range []string{binding.TenantID, binding.OwnerID} {
		if value == "" || !utf8.ValidString(value) {
			return nil, errInvalidBinding
		}
	}
	return json.Marshal(struct {
		Domain  string                  `json:"domain"`
		Version byte                    `json:"version"`
		Binding EnvironmentSetupBinding `json:"binding"`
	}{"parsar.agents-api.environment-setup", formatVersion, binding})
}
