package opencode_test

import (
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/opencode"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/versionprobe/testutil"
)

func TestCheckCLIAvailableContract(t *testing.T) {
	testutil.RunContract(t, testutil.Contract{
		Name:               "opencode",
		DefaultBinary:      "opencode",
		MissingError:       opencode.ErrCLINotFound,
		Check:              opencode.CheckCLIAvailable,
		WhitespaceDefaults: true,
	})
}
