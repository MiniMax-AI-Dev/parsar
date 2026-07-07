// Package bootstrap provisions the first workspace owner on a fresh
// install. Single-use: once an active owner exists, both the HTTP
// endpoint and the store primitive return store.ErrBootstrapClosed.
// Recovery of a lost owner requires DB-level operations.
//
// HTTP POST /api/v1/bootstrap is unauthenticated by design: the gate
// is `count(active workspace owners) == 0`, enforced under a Postgres
// advisory lock inside the tx. This mirrors coder's first-user setup
// flow (coderd/users.go firstUser/postFirstUser) and removes the
// PARSAR_BOOTSTRAP_TOKEN chicken-and-egg problem for open-source users.
//
// GET /api/v1/bootstrap/status returns boolean state only so the
// installer UI can decide between "show registration" and "show login".
package bootstrap

import (
	"context"
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
	repo      Repo
	clock     func() time.Time
	publicURL string
}

type Option func(*Service)

func WithClock(now func() time.Time) Option {
	return func(s *Service) {
		if now != nil {
			s.clock = now
		}
	}
}

// WithPublicURL records the operator-configured public URL so Status
// can hand it to the web UI. The UI mints the daemon one-line connect
// command from this value rather than the request Host / X-Forwarded-Host
// header, which a client controls and could spoof.
func WithPublicURL(u string) Option {
	return func(s *Service) {
		s.publicURL = u
	}
}

// NewService constructs a Service.
func NewService(repo Repo, opts ...Option) *Service {
	svc := &Service{
		repo:  repo,
		clock: func() time.Time { return time.Now().UTC() },
	}
	for _, opt := range opts {
		opt(svc)
	}
	return svc
}

var ErrInvalidInput = errors.New("bootstrap: invalid input")

// StatusResult is the JSON payload of GET /api/v1/bootstrap/status.
// DevAuthEnabled lets the installer UI warn if the dev_auth
// middleware shim is on in a production-ish env.
type StatusResult struct {
	Needed         bool   `json:"needed"`
	HasOwners      bool   `json:"has_owners"`
	OwnerCount     int64  `json:"owner_count"`
	DevAuthEnabled bool   `json:"dev_auth_enabled"`
	PublicURL      string `json:"public_url"`
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
		DevAuthEnabled: devAuth,
		PublicURL:      s.publicURL,
	}, nil
}

// Create runs first-owner provisioning. The gate lives inside
// store.ProvisionFirstOwner (advisory lock + owner-count check), so
// this method only validates surface inputs and delegates. Errors
// map to HTTP statuses in the handler layer:
//
//	ErrInvalidInput            -> 400
//	store.ErrBootstrapClosed   -> 409
//	(other)                    -> 500
func (s *Service) Create(ctx context.Context, in store.ProvisionFirstOwnerInput) (store.ProvisionFirstOwnerResult, error) {
	if s == nil || s.repo == nil {
		return store.ProvisionFirstOwnerResult{}, errors.New("bootstrap: service not configured")
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
