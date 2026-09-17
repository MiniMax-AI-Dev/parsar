//go:build !linux

package placement

import (
	"context"
	"errors"
)

// Controller requires the qualified local Linux supervisor profile.
type Controller struct{}

// New rejects unqualified hosts before any supervisor operation.
func New() (*Controller, error) {
	return nil, errors.New("placement retirement requires local Linux/Docker with cgroup v2")
}

func (*Controller) enroll(context.Context, string, string, string, string) (*Receipt, error) {
	return nil, errors.New("unsupported placement host")
}

func (*Controller) retirePlacement(context.Context, string, string) (*Receipt, error) {
	return nil, errors.New("unsupported placement host")
}
