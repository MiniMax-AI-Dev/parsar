package deepseekharness_test

import (
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/deepseekharness"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/versionprobe/testutil"
)

func TestCheckCLIAvailableContract(t *testing.T) {
	testutil.RunContract(t, testutil.Contract{
		Name:               "dsh",
		DefaultBinary:      "dsh",
		MissingError:       deepseekharness.ErrCLINotFound,
		Check:              deepseekharness.CheckCLIAvailable,
		WhitespaceDefaults: true,
	})
}
