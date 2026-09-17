package placement

import (
	"context"
	"errors"
	"github.com/google/uuid"
)

func validEnvironment(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed != uuid.Nil && parsed.String() == id
}

// Enroll records an unscoped operator placement without Environment authority.
func (c *Controller) Enroll(ctx context.Context, id, owner, workspace string) (*Receipt, error) {
	return c.enroll(ctx, id, owner, workspace, "")
}

// EnrollEnvironment records an immutable operator-confirmed Environment association.
func (c *Controller) EnrollEnvironment(ctx context.Context, id, owner, workspace, environment string) (*Receipt, error) {
	if !validEnvironment(environment) {
		return nil, errors.New("invalid placement Environment ID")
	}
	return c.enroll(ctx, id, owner, workspace, environment)
}

// Retire reconciles only unscoped operator enrollment.
func (c *Controller) Retire(ctx context.Context, id string) (*Receipt, error) {
	return c.retirePlacement(ctx, id, "")
}

// RetireEnvironment requires the same Environment before any supervisor access.
func (c *Controller) RetireEnvironment(ctx context.Context, id, environment string) (*Receipt, error) {
	if !validEnvironment(environment) {
		return nil, errors.New("invalid placement Environment ID")
	}
	return c.retirePlacement(ctx, id, environment)
}
