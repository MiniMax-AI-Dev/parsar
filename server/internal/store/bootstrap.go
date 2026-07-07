package store

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/audit"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

// ErrBootstrapClosed is returned by ProvisionFirstOwner when the
// database already has at least one active workspace owner. The
// bootstrap endpoint maps this to HTTP 409 Conflict.
var ErrBootstrapClosed = errors.New("bootstrap already complete: at least one workspace owner exists")

// bootstrapAdvisoryLockKey is the pg_advisory_xact_lock key that
// serializes concurrent first-owner provisioning. Without it, two
// simultaneous bootstrap calls under READ COMMITTED can both observe
// owner_count==0 and each successfully insert an owner row.
//
// Derived from sha256("parsar:bootstrap-first-owner")[:8] interpreted
// as int64. Future advisory locks should follow the same recipe with a
// distinct label string.
var bootstrapAdvisoryLockKey int64

func init() {
	sum := sha256.Sum256([]byte("parsar:bootstrap-first-owner"))
	bootstrapAdvisoryLockKey = int64(binary.BigEndian.Uint64(sum[:8]))
}

// ProvisionFirstOwnerInput is the payload for the very first
// owner+workspace provisioning. The HTTP handler validates fields
// before reaching here.
//
// PasswordHash is optional: when non-empty, the tx also writes an
// auth_identities(provider='email') row so the owner can log in via
// POST /api/v1/auth/login afterwards. Leaving it empty preserves the
// pre-existing "bootstrap without local password" path (feishu-only
// deployments and the parsar-bootstrap CLI when password is omitted).
type ProvisionFirstOwnerInput struct {
	Email         string
	Name          string // defaults to local part of email
	WorkspaceName string // slug auto-generated
	PasswordHash  string // bcrypt output; empty = no email identity written
	Now           time.Time
}

// ProvisionFirstOwnerResult returns the UUID of the new user and
// workspace, plus the system-generated slug.
type ProvisionFirstOwnerResult struct {
	UserID        string
	UserCreated   bool // true = inserted; false = pre-existing user reused
	WorkspaceID   string
	WorkspaceSlug string
	WorkspaceName string
	MemberID      string
}

// ProvisionFirstOwner is the bootstrap primitive: inside one tx it
// confirms no active owner exists, upserts the user by email, creates
// the workspace with an auto-generated slug, and inserts the owner
// membership row.
//
// Per-user onboarding goes through the admin shell's `/onboarding`
// flow against CreateWorkspace, not this primitive.
//
// Audit: emits "bootstrap.first_owner_created" with workspace_id +
// user_email + slug.
func (s *Store) ProvisionFirstOwner(ctx context.Context, input ProvisionFirstOwnerInput) (ProvisionFirstOwnerResult, error) {
	// Normalize BEFORE the "@" check so a mixed-case email is accepted
	// and stored in the canonical form the login path queries with.
	email := normalizeEmail(input.Email)
	if email == "" {
		return ProvisionFirstOwnerResult{}, fmt.Errorf("%w: email is required", ErrInvalidWorkspaceInput)
	}
	if !strings.Contains(email, "@") {
		return ProvisionFirstOwnerResult{}, fmt.Errorf("%w: email is not a valid address: %s", ErrInvalidWorkspaceInput, email)
	}
	workspaceName := strings.TrimSpace(input.WorkspaceName)
	if workspaceName == "" {
		return ProvisionFirstOwnerResult{}, fmt.Errorf("%w: workspace_name is required", ErrInvalidWorkspaceInput)
	}
	userName := strings.TrimSpace(input.Name)
	if userName == "" {
		if at := strings.Index(email, "@"); at > 0 {
			userName = email[:at]
		} else {
			userName = email
		}
	}
	now := input.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	tx, err := beginTx(ctx, s.db)
	if err != nil {
		return ProvisionFirstOwnerResult{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New(tx)

	// Advisory lock BEFORE the owner-count read: under READ COMMITTED
	// two simultaneous callers can both see count==0 and each succeed.
	// Released automatically on tx commit/rollback.
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", bootstrapAdvisoryLockKey); err != nil {
		return ProvisionFirstOwnerResult{}, fmt.Errorf("acquire bootstrap advisory lock: %w", err)
	}

	ownerCount, err := q.CountActiveWorkspaceOwners(ctx)
	if err != nil {
		return ProvisionFirstOwnerResult{}, fmt.Errorf("count active owners: %w", err)
	}
	if ownerCount > 0 {
		return ProvisionFirstOwnerResult{}, ErrBootstrapClosed
	}

	userRow, err := q.UpsertUserByEmail(ctx, sqlc.UpsertUserByEmailParams{
		ID:    mustUUID(newID()),
		Email: email,
		Name:  userName,
		Now:   timestamptz(now),
	})
	if err != nil {
		return ProvisionFirstOwnerResult{}, fmt.Errorf("upsert user: %w", err)
	}

	// Bind local email/password identity when the caller supplied a
	// pre-hashed password. Stays inside the tx so a failure here
	// rolls back the fresh user row too.
	if input.PasswordHash != "" {
		metaBytes, mErr := json.Marshal(map[string]string{
			"password_hash": input.PasswordHash,
			"hashed_at":     now.Format(time.RFC3339),
		})
		if mErr != nil {
			return ProvisionFirstOwnerResult{}, fmt.Errorf("marshal email identity metadata: %w", mErr)
		}
		if err := q.UpsertEmailPasswordIdentity(ctx, sqlc.UpsertEmailPasswordIdentityParams{
			ID:       mustUUID(newID()),
			UserID:   mustUUID(userRow.ID),
			Email:    email,
			Metadata: metaBytes,
			Now:      timestamptz(now),
		}); err != nil {
			return ProvisionFirstOwnerResult{}, fmt.Errorf("upsert email identity: %w", err)
		}
	}

	var (
		wsRow sqlc.CreateWorkspaceRow
		slug  string
	)
	for attempt := 0; attempt < autoSlugMaxAttempts; attempt++ {
		candidate := generateAutoSlug("workspace")
		exists, existsErr := q.WorkspaceSlugExists(ctx, candidate)
		if existsErr != nil {
			return ProvisionFirstOwnerResult{}, fmt.Errorf("workspace slug exists check: %w", existsErr)
		}
		if exists {
			continue
		}
		slug = candidate
		wsRow, err = q.CreateWorkspace(ctx, sqlc.CreateWorkspaceParams{
			ID:         mustUUID(newID()),
			Name:       workspaceName,
			Slug:       slug,
			Visibility: workspaceVisibilityPrivate,
			CreatedBy:  mustUUID(userRow.ID),
			Now:        timestamptz(now),
		})
		if err == nil {
			break
		}
		if attempt == autoSlugMaxAttempts-1 || !isUniqueViolation(err) {
			return ProvisionFirstOwnerResult{}, fmt.Errorf("create workspace: %w", err)
		}
	}
	if wsRow.ID == "" {
		return ProvisionFirstOwnerResult{}, fmt.Errorf("%w: could not generate unique workspace slug after %d attempts", ErrDuplicateWorkspaceSlug, autoSlugMaxAttempts)
	}

	memberRow, err := q.AddWorkspaceMember(ctx, sqlc.AddWorkspaceMemberParams{
		ID:            mustUUID(newID()),
		WorkspaceID:   mustUUID(wsRow.ID),
		UserID:        mustUUID(userRow.ID),
		Role:          memberRoleOwner,
		Status:        memberStatusActive,
		RequestReason: "",
		Now:           timestamptz(now),
	})
	if err != nil {
		return ProvisionFirstOwnerResult{}, fmt.Errorf("add owner: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return ProvisionFirstOwnerResult{}, err
	}

	s.emitAuditEvent(audit.Event{
		OccurredAt:  now,
		Source:      audit.SourceAdmin,
		EventType:   "bootstrap.first_owner_created",
		ActorType:   audit.ActorTypeSystem,
		ActorID:     userRow.ID,
		TargetType:  "workspace",
		TargetID:    wsRow.ID,
		WorkspaceID: wsRow.ID,
		Payload: map[string]any{
			"source":       "bootstrap",
			"workspace_id": wsRow.ID,
			"user_email":   userRow.Email,
			"user_created": userRow.Created,
			"slug":         wsRow.Slug,
			"name":         wsRow.Name,
		},
	})

	return ProvisionFirstOwnerResult{
		UserID:        userRow.ID,
		UserCreated:   userRow.Created,
		WorkspaceID:   wsRow.ID,
		WorkspaceSlug: wsRow.Slug,
		WorkspaceName: wsRow.Name,
		MemberID:      memberRow.ID,
	}, nil
}

// ActiveWorkspaceOwnerCount returns the number of (workspace, user)
// owner memberships that are active and whose workspace is also active.
// Used by the bootstrap status endpoint.
func (s *Store) ActiveWorkspaceOwnerCount(ctx context.Context) (int64, error) {
	q := sqlc.New(s.db)
	return q.CountActiveWorkspaceOwners(ctx)
}
