package claudecode_test

import (
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/claudecode"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/versionprobe/testutil"
)

func TestCheckCLIAvailableContract(t *testing.T) {
	testutil.RunContract(t, testutil.Contract{
		Name:          "claude",
		DefaultBinary: "claude",
		MissingError:  claudecode.ErrCLINotFound,
		Check:         claudecode.CheckCLIAvailable,
	})
}
