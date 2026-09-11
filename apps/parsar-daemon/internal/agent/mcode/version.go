package mcode

import (
	"context"
	"errors"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/binpath"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/versionprobe"
)

var ErrCLINotFound = errors.New("mcode CLI not found")

func defaultBinary() string { return binpath.MCode() }

func CheckCLIAvailable(ctx context.Context, binary string) (string, error) {
	return versionprobe.Check(ctx, binary, versionprobe.Config{Name: "mcode", DefaultBinary: defaultBinary(), MissingError: ErrCLINotFound, TrimBinary: true})
}
