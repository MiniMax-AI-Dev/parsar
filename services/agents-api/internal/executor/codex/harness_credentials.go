package codex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

const maxHarnessCredentials = 32

var ErrHarnessCapacity = errors.New("native harness credential capacity reached")

type harnessCredential struct {
	key   ScopedKey
	hash  [32]byte
	owner context.Context
	stop  func() bool
}

// IssueHarnessCredential authorizes one execution owner, spanning preparation and its Run.
// The caller must release ownership after execution; cancellation also revokes it.
// Only the returned bearer is secret. The registry retains its digest, never the bearer.
func (r *Registry) IssueHarnessCredential(owner context.Context, tenant, environment string) (string, func(), error) {
	tenantID, tenantErr := uuid.Parse(tenant)
	environmentID, environmentErr := uuid.Parse(environment)
	if tenantErr != nil || environmentErr != nil || tenantID == uuid.Nil || environmentID == uuid.Nil {
		return "", nil, store.ErrInvalidInput
	}
	key := ScopedKey{TenantID: tenantID.String(), EnvironmentID: environmentID.String()}
	if err := r.authorized(owner, key); err != nil {
		return "", nil, err
	}
	token, err := capability()
	if err != nil {
		return "", nil, err
	}
	hash := sha256.Sum256([]byte(token))
	key.TokenSHA256 = hex.EncodeToString(hash[:])
	credential := &harnessCredential{key: key, hash: hash, owner: owner}
	release := func() { r.releaseHarnessCredential(credential) }
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := owner.Err(); err != nil {
		return "", nil, err
	}
	if r.closed {
		return "", nil, errors.New("native executor registry is closed")
	}
	if len(r.harnessKeys) >= maxHarnessCredentials {
		return "", nil, ErrHarnessCapacity
	}
	if r.harnessKeys[hash] != nil {
		return "", nil, errors.New("native harness credential collision")
	}
	r.harnessKeys[hash] = credential
	credential.stop = context.AfterFunc(owner, release)
	return token, release, nil
}

func (r *Registry) harnessCredential(req *http.Request, environment string) (*harnessCredential, bool) {
	hash, ok := credentialHash(req)
	if !ok {
		return nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	credential := r.harnessKeys[hash]
	return credential, r.harnessCredentialActiveLocked(credential) && credential.key.EnvironmentID == environment
}

func (r *Registry) harnessCredentialActiveLocked(credential *harnessCredential) bool {
	return !r.closed && credential != nil && credential.owner.Err() == nil && r.harnessKeys[credential.hash] == credential
}

func (r *Registry) releaseHarnessCredential(credential *harnessCredential) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.harnessKeys[credential.hash] != credential {
		return
	}
	delete(r.harnessKeys, credential.hash)
	credential.stop()
	reg := r.registrations[credential.key.EnvironmentID]
	if reg == nil {
		return
	}
	for ticket, grant := range reg.grants {
		if grant.credential == credential {
			delete(reg.grants, ticket)
			if grant.harness != nil {
				r.closeConnectionLocked(reg, grant.harness)
			}
		}
	}
}
