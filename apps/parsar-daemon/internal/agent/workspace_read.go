package agent

import (
	"context"
	"errors"
)

type WorkspaceReadResult struct {
	Data      []byte
	Truncated bool
}

// WorkspaceReader returns success only after acknowledged native close on an existing owner.
type WorkspaceReader interface {
	ReadWorkspaceFile(context.Context, string, int) (WorkspaceReadResult, error)
}

var (
	ErrWorkspaceReadUnsupported = errors.New("workspace read unsupported")
	ErrWorkspaceReadUnavailable = errors.New("workspace read unavailable")
	ErrWorkspaceReadBusy        = errors.New("workspace read busy")
	ErrWorkspaceReadInvalid     = errors.New("workspace read invalid")
	ErrWorkspaceReadUncertain   = errors.New("workspace read outcome uncertain")
)
