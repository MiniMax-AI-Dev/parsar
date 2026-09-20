package credentialcrypto

import (
	"encoding/json"
	"unicode/utf8"
)

func modelExecutionData(tenantID, sessionID string) ([]byte, error) {
	if tenantID == "" || sessionID == "" || !utf8.ValidString(tenantID) || !utf8.ValidString(sessionID) {
		return nil, errInvalidBinding
	}
	return json.Marshal([]string{"parsar.agents-api.session-model-execution.v1", tenantID, sessionID})
}

func (c *Cipher) SealModelExecution(plaintext []byte, tenantID, sessionID string) ([]byte, error) {
	aad, err := modelExecutionData(tenantID, sessionID)
	if err != nil {
		return nil, err
	}
	return c.seal(plaintext, aad)
}

func (c *Cipher) OpenModelExecution(ciphertext []byte, tenantID, sessionID string) ([]byte, error) {
	aad, err := modelExecutionData(tenantID, sessionID)
	if err != nil {
		return nil, err
	}
	return c.open(ciphertext, aad)
}
