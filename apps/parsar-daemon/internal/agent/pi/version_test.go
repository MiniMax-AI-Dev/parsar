package pi_test

import (
	"context"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/pi"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/versionprobe/testutil"
)

func TestCheckCLIAvailableContract(t *testing.T) {
	testutil.RunContract(t, testutil.Contract{
		Name:               "pi",
		DefaultBinary:      "pi",
		MissingError:       pi.ErrCLINotFound,
		Check:              pi.CheckCLIAvailable,
		WhitespaceDefaults: true,
	})
}

func TestCheckCLIAvailableFallsBackToStderr(t *testing.T) {
	stub := testutil.WriteStub(t, "stderr-pi", "#!/bin/sh\nprintf '  pi 0.74.2\\nextra line  \\n' 1>&2\n")
	version, err := pi.CheckCLIAvailable(context.Background(), stub)
	if err != nil {
		t.Fatalf("check CLI: %v", err)
	}
	if version != "pi 0.74.2" {
		t.Fatalf("version = %q, want %q", version, "pi 0.74.2")
	}
}
