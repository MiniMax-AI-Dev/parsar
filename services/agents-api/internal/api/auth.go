package api

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/identity"
)

// APIKey binds a service credential to an execution principal, not a product user.
// Configuration stores the SHA-256 hex digest, never the plaintext key.
type APIKey struct {
	TokenSHA256    string `json:"token_sha256"`
	TenantID       string `json:"tenant_id"`
	OrganizationID string `json:"organization_id"`
	ProjectID      string `json:"project_id"`
	SubjectKind    string `json:"subject_kind"`
	SubjectID      string `json:"subject_id"`
}

type Authenticator struct {
	principals map[[32]byte]identity.Principal
	projects   []identity.ProjectScope
}

func NewAuthenticator(keys []APIKey) (*Authenticator, error) {
	if len(keys) == 0 {
		return nil, errors.New("at least one Agents API key is required")
	}
	a := &Authenticator{principals: make(map[[32]byte]identity.Principal, len(keys))}
	for _, key := range keys {
		principal := identity.Principal{ProjectScope: identity.ProjectScope{TenantID: key.TenantID, OrganizationID: key.OrganizationID, ProjectID: key.ProjectID}, SubjectKind: key.SubjectKind, SubjectID: key.SubjectID}
		if err := principal.Validate(); err != nil {
			return nil, err
		}
		digest, err := hex.DecodeString(key.TokenSHA256)
		if err != nil || len(digest) != sha256.Size {
			return nil, errors.New("API key token_sha256 must be a SHA-256 hex digest")
		}
		hash := [32]byte(digest)
		if _, exists := a.principals[hash]; exists {
			return nil, errors.New("duplicate API key digest")
		}
		a.principals[hash] = principal
		a.projects = append(a.projects, principal.ProjectScope)
	}
	var err error
	a.projects, err = identity.ProjectScopes(a.projects)
	if err != nil {
		return nil, err
	}
	return a, nil
}

func (a *Authenticator) ProjectScopes() []identity.ProjectScope {
	return append([]identity.ProjectScope(nil), a.projects...)
}

func (a *Authenticator) principal(r *http.Request) (identity.Principal, bool) {
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(r.Header.Values("Authorization")) != 1 || len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return identity.Principal{}, false
	}
	principal, ok := a.principals[sha256.Sum256([]byte(parts[1]))]
	if !ok || !matchesScopeHeader(r, "OpenAI-Organization", principal.OrganizationID) || !matchesScopeHeader(r, "OpenAI-Project", principal.ProjectID) {
		return identity.Principal{}, false
	}
	return principal, true
}

func matchesScopeHeader(r *http.Request, name, expected string) bool {
	values := r.Header.Values(name)
	return len(values) == 0 || (len(values) == 1 && values[0] == expected)
}
