// Package sandbox manages compute for an already authorized Environment.
// It does not schedule Turns or implement routine execution and Files operations.
package sandbox

import (
	"context"
	"errors"
)

var (
	ErrInvalid            = errors.New("invalid sandbox configuration")
	ErrOwnership          = errors.New("sandbox ownership mismatch")
	ErrExists             = errors.New("sandbox allocation already exists")
	ErrNotFound           = errors.New("sandbox allocation not found")
	ErrCommandUnconfirmed = errors.New("initialization command outcome unconfirmed; reclaim allocation before reuse")
)

// Reference must be persisted by the caller before Create. AllocationID is a fresh
// UUID for one attempt, not the Environment ID. Serialize lifecycle operations for
// an allocation; a lost Create response is resolved with GetInfo, never by replay.
type Reference struct{ TenantID, EnvironmentID, AllocationID string }

type Bootstrap struct {
	Reference
	SessionID, DeviceID, CoreURL, Credential string
	NetworkAccess                            string
}

// Info describes compute only. Running does not establish daemon authentication,
// native preparation, Environment readiness or a renewable provider lease.
type Info struct {
	Reference
	ProviderID, State string
	// BootstrapComplete is provider evidence that initialization has reached its
	// last mutating step. It does not establish daemon or native readiness.
	BootstrapComplete bool
}
type Command struct {
	Args      []string
	Directory string
}
type CommandResult struct {
	Stdout, Stderr string
	ExitCode       int
}

type Provider interface {
	Create(context.Context, Bootstrap) (Info, error)
	GetInfo(context.Context, Reference) (Info, error)
	Renew(context.Context, Reference) (Info, error)
	Kill(context.Context, Reference) error
	RunCommand(context.Context, Reference, Command) (CommandResult, error)
}
