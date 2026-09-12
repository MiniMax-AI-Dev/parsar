//go:build !unix

package codex

import (
	"context"
	"errors"
	"os/exec"
)

// SupportsTextVerbosity requires bounded cancellation of the catalog probe.
const SupportsTextVerbosity = false

func modelCatalogCommand(context.Context, string, ...string) (*exec.Cmd, error) {
	return nil, errors.New("codex: text verbosity requires Unix process-group cancellation support")
}
