package execution

import (
	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"testing"
)

func TestExecutionEnvironmentDoesNotDefaultToLocal(t *testing.T) {
	cases := []struct {
		name          string
		snapshot      Snapshot
		dir           string
		none, invalid bool
	}{
		{"public none", Snapshot{Environment: &v1.Environment{Type: "none"}}, "", true, false},
		{"legacy device", Snapshot{Daemon: &DaemonConfig{WorkDir: "/workspace"}}, "/workspace", false, false},
		{"missing", Snapshot{}, "", false, true},
		{"conflicting", Snapshot{Environment: &v1.Environment{Type: "none"}, Daemon: &DaemonConfig{}}, "", false, true},
		{"unsupported", Snapshot{Environment: &v1.Environment{Type: "self_hosted"}}, "", false, true},
		{"relative", Snapshot{Daemon: &DaemonConfig{WorkDir: "workspace"}}, "", false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, none, err := resolveExecutionEnvironment(c.snapshot)
			if (err != nil) != c.invalid || dir != c.dir || none != c.none {
				t.Fatal(dir, none, err)
			}
		})
	}
}
