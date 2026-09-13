package codex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func (r *Registry) executorCredential(w http.ResponseWriter, req *http.Request, environment string) (ScopedKey, bool) {
	parts := strings.Fields(req.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		writeError(w, http.StatusUnauthorized)
		return ScopedKey{}, false
	}
	hash := sha256.Sum256([]byte(parts[1]))
	if _, wrongPurpose := r.harnessKeys[hash]; wrongPurpose {
		writeError(w, http.StatusUnauthorized)
		return ScopedKey{}, false
	}
	ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
	defer cancel()
	digest := hex.EncodeToString(hash[:])
	tenant, err := r.source.AuthenticateEnvironmentExecutor(ctx, environment, digest)
	if !writeCredentialError(w, err) {
		return ScopedKey{}, false
	}
	return ScopedKey{TenantID: tenant, EnvironmentID: environment, TokenSHA256: digest}, true
}

func (r *Registry) currentExecutor(ctx context.Context, key ScopedKey) error {
	tenant, err := r.source.AuthenticateEnvironmentExecutor(ctx, key.EnvironmentID, key.TokenSHA256)
	if err == nil && tenant != key.TenantID {
		return store.ErrNotFound
	}
	return err
}

func (r *Registry) checkCurrentExecutor(w http.ResponseWriter, req *http.Request, key ScopedKey) bool {
	ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
	defer cancel()
	return writeCredentialError(w, r.currentExecutor(ctx, key))
}

func (r *Registry) executorAuthorized(ctx context.Context, key ScopedKey) error {
	if err := r.authorized(ctx, key); err != nil {
		return err
	}
	return r.currentExecutor(ctx, key)
}

func writeCredentialError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return true
	}
	status := http.StatusServiceUnavailable
	if errors.Is(err, store.ErrNotFound) {
		status = http.StatusUnauthorized
	}
	writeError(w, status)
	return false
}
