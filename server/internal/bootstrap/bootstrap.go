// Package bootstrap provisions the first workspace owner on a fresh
// install. Single-use: once an active owner exists, both the HTTP
// endpoint and store primitive return store.ErrBootstrapClosed.
// Recovery of a lost owner requires DB-level operations.
//
// HTTP POST /api/v1/bootstrap requires Authorization: Bearer <token>
// where <token> is config.Auth.Bootstrap.Token; empty token → 503 and
// operators use the CLI (server/cmd/parsar-bootstrap), which reads
// DATABASE_URL directly and skips the token check.
//
// GET /api/v1/bootstrap/status is unauthenticated by design — the
// installer needs to know if setup is required before any token
// exists. The response returns boolean state only, no identity.
package bootstrap

import (
	"context"
	"crypto/subtle"
	"errors"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type Repo interface {
	ActiveWorkspaceOwnerCount(ctx context.Context) (int64, error)
	ProvisionFirstOwner(ctx context.Context, in store.ProvisionFirstOwnerInput) (store.ProvisionFirstOwnerResult, error)
}

type Service struct {
	repo  Repo
	token []byte
	clock func() time.Time
}

type Option func(*Service)

func WithClock(now func() time.Time) Option {
	return func(s *Service) {
		if now != nil {
			s.clock = now
		}
	}
}

// NewService constructs a Service. Empty token disables the HTTP
// surface (Create returns ErrHTTPDisabled); the CLI carrier passes
// empty and relies on its own access path.
func NewService(repo Repo, token string, opts ...Option) *Service {
	svc := &Service{
		repo:  repo,
		token: []byte(token),
		clock: func() time.Time { return time.Now().UTC() },
	}
	for _, opt := range opts {
		opt(svc)
	}
	return svc
}

var ErrHTTPDisabled = errors.New("bootstrap: HTTP endpoint disabled (PARSAR_BOOTSTRAP_TOKEN unset)")

var ErrUnauthorized = errors.New("bootstrap: invalid token")

var ErrInvalidInput = errors.New("bootstrap: invalid input")

// StatusResult is the JSON payload of GET /api/v1/bootstrap/status.
// DevAuthEnabled lets the installer UI warn if the dev_auth
// middleware shim is on in a production-ish env.
type StatusResult struct {
	Needed         bool  `json:"needed"`
	HasOwners      bool  `json:"has_owners"`
	OwnerCount     int64 `json:"owner_count"`
	HTTPEnabled    bool  `json:"http_enabled"`
	DevAuthEnabled bool  `json:"dev_auth_enabled"`
}

// Status returns the current bootstrap posture. devAuth is supplied
// by the handler (reads cfg.Auth.DevAuth) so this package does not
// import config.
func (s *Service) Status(ctx context.Context, devAuth bool) (StatusResult, error) {
	if s == nil || s.repo == nil {
		return StatusResult{}, errors.New("bootstrap: service not configured")
	}
	count, err := s.repo.ActiveWorkspaceOwnerCount(ctx)
	if err != nil {
		return StatusResult{}, err
	}
	return StatusResult{
		Needed:         count == 0,
		HasOwners:      count > 0,
		OwnerCount:     count,
		HTTPEnabled:    len(s.token) > 0,
		DevAuthEnabled: devAuth,
	}, nil
}

// Create runs first-owner provisioning. Token comparison uses
// crypto/subtle.ConstantTimeCompare. Errors map to HTTP statuses in
// the handler layer:
//
//	ErrHTTPDisabled            -> 503
//	ErrUnauthorized            -> 401
//	ErrInvalidInput            -> 400
//	store.ErrBootstrapClosed   -> 409
//	(other)                    -> 500
func (s *Service) Create(ctx context.Context, providedToken string, in store.ProvisionFirstOwnerInput) (store.ProvisionFirstOwnerResult, error) {
	if s == nil || s.repo == nil {
		return store.ProvisionFirstOwnerResult{}, errors.New("bootstrap: service not configured")
	}
	if len(s.token) == 0 {
		return store.ProvisionFirstOwnerResult{}, ErrHTTPDisabled
	}
	provided := []byte(strings.TrimSpace(providedToken))
	if subtle.ConstantTimeCompare(provided, s.token) != 1 {
		return store.ProvisionFirstOwnerResult{}, ErrUnauthorized
	}
	if strings.TrimSpace(in.Email) == "" {
		return store.ProvisionFirstOwnerResult{}, errInvalid("email is required")
	}
	if !strings.Contains(in.Email, "@") {
		return store.ProvisionFirstOwnerResult{}, errInvalid("email is not a valid address")
	}
	if strings.TrimSpace(in.WorkspaceName) == "" {
		return store.ProvisionFirstOwnerResult{}, errInvalid("workspace_name is required")
	}
	if in.Now.IsZero() {
		in.Now = s.clock()
	}
	out, err := s.repo.ProvisionFirstOwner(ctx, in)
	if err != nil {
		return store.ProvisionFirstOwnerResult{}, err
	}
	return out, nil
}

// errInvalid wraps a user-facing reason so the handler maps to 400
// with the reason surfaced in the JSON body.
func errInvalid(reason string) error {
	return invalidErr{reason: reason}
}

type invalidErr struct{ reason string }

func (e invalidErr) Error() string { return "bootstrap: " + e.reason }
func (e invalidErr) Is(target error) bool {
	return target == ErrInvalidInput
}
