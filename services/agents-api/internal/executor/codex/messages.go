package codex

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
)

const securityProfile = "noise_hybrid_ik_v1"
const noiseSuite = "Noise_hybridIK_X25519+MLKEM768_AESGCM_SHA256"

// PublicKey follows the pinned native exec-server NoiseChannelPublicKey wire type.
type PublicKey struct {
	Suite    string `json:"suite"`
	X25519   string `json:"x25519_public_key"`
	MLKEM768 string `json:"mlkem768_public_key"`
}

func (k PublicKey) valid() bool {
	dh, err := base64.StdEncoding.Strict().DecodeString(k.X25519)
	kem, kemErr := base64.StdEncoding.Strict().DecodeString(k.MLKEM768)
	return k.Suite == noiseSuite && err == nil && len(dh) == 32 && kemErr == nil && len(kem) == 1184
}

type RegistrationRequest struct {
	SecurityProfile   string    `json:"security_profile"`
	ExecutorPublicKey PublicKey `json:"executor_public_key"`
}

type RegistrationResponse struct {
	EnvironmentID          string `json:"environment_id"`
	URL                    string `json:"url"`
	SecurityProfile        string `json:"security_profile"`
	ExecutorRegistrationID string `json:"executor_registration_id"`
}

type RegistryError struct {
	Error ErrorDetail `json:"error"`
}
type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int) {
	writeJSON(w, status, RegistryError{Error: ErrorDetail{Code: "executor_registry_error", Message: http.StatusText(status)}})
}
