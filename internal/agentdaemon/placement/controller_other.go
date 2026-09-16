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

// Enroll is unavailable on unqualified hosts.
func (*Controller) Enroll(context.Context, string, string, string) (*Receipt, error) {
	return nil, errors.New("unsupported placement host")
}

// Retire is unavailable on unqualified hosts.
func (*Controller) Retire(context.Context, string) (*Receipt, error) {
	return nil, errors.New("unsupported placement host")
}
