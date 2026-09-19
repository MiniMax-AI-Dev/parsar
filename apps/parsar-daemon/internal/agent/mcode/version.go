package mcode

import (
	"context"
	"errors"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/binpath"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/versionprobe"
)

var ErrCLINotFound = errors.New("mcode CLI not found")

const SupportedVersion = "0.4.12"

func defaultBinary() string { return binpath.MCode() }

func CheckCLIAvailable(ctx context.Context, binary string) (string, error) {
	version, err := versionprobe.Check(ctx, binary, versionprobe.Config{Name: "mcode", DefaultBinary: defaultBinary(), MissingError: ErrCLINotFound, TrimBinary: true})
	if err == nil && version != SupportedVersion {
		err = fmt.Errorf("mcode: unsupported version %s; install %s", version, SupportedVersion)
	}
	return version, err
}
