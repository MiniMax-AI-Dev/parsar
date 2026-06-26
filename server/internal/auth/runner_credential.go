package auth

// Bearer-credential middleware for the /api/v1/agent-runtime/* surface.
// A single runtime token authenticates both parsar-daemon and the in-sandbox
// CLI; these endpoints do not carry a runtime id in the URL, so the
// bearer is the only credential dimension.

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// RunnerIdentityResolver follows the store's "not found is not an
// error" convention: (zero, false, nil) means no such credential,
// non-nil error means lookup itself failed. The middleware maps
// both to 401 but separating them lets tests assert each path.
type RunnerIdentityResolver interface {
	ResolveRuntimeIdentity(ctx context.Context, plaintext string) (store.RuntimeIdentity, bool, error)
}

type runtimeIdentityCtxKey struct{}

func WithRuntimeIdentity(ctx context.Context, id store.RuntimeIdentity) context.Context {
	return context.WithValue(ctx, runtimeIdentityCtxKey{}, id)
}

func RuntimeIdentityFromContext(ctx context.Context) (store.RuntimeIdentity, bool) {
	id, ok := ctx.Value(runtimeIdentityCtxKey{}).(store.RuntimeIdentity)
	return id, ok
}

// RunnerCredentialOptions configures the middleware. The middleware
// does NOT log on 401 — otherwise log-line absence becomes a probe
// oracle for valid hashes.
type RunnerCredentialOptions struct {
	Logger *slog.Logger
}

// RunnerCredential validates a Bearer credential against the resolver.
// Fails closed: missing/wrong-scheme/empty/unknown → 401; resolver
// error → 500. The 401 body is intentionally generic so an attacker
// cannot distinguish "no credential" from "wrong credential" by
// response shape.
func RunnerCredential(resolver RunnerIdentityResolver, opts RunnerCredentialOptions) func(http.Handler) http.Handler {
	if resolver == nil {
		panic("auth: RunnerCredential requires a non-nil resolver")
	}
	logger := opts.Logger
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r)
			if !ok {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			id, found, err := resolver.ResolveRuntimeIdentity(r.Context(), token)
			if err != nil {
				// Log the resolver error WITHOUT the presented
				// token — even partial bearer leakage to logs is
				// unacceptable.
				if logger != nil {
					logger.LogAttrs(r.Context(), slog.LevelError,
						"runner credential resolver error",
						slog.String("err", err.Error()))
				}
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			if !found {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r.WithContext(WithRuntimeIdentity(r.Context(), id)))
		})
	}
}

var ErrNoRunnerCredential = errors.New("auth: no runner credential on request")

// bearerToken extracts the token from Authorization. Returns
// ("", false) when missing, wrong scheme, or empty/whitespace token.
func bearerToken(r *http.Request) (string, bool) {
	raw := r.Header.Get("Authorization")
	if raw == "" {
		return "", false
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(raw, prefix) {
		return "", false
	}
	token := strings.TrimSpace(raw[len(prefix):])
	if token == "" {
		return "", false
	}
	return token, true
}

// Compile-time guard: *store.Store satisfies the resolver interface.
var _ RunnerIdentityResolver = (*store.Store)(nil)
