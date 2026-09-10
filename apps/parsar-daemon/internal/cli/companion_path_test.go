package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCompanionCLIPath(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode os.FileMode
		want bool
	}{
		{"executable", 0o700, true},
		{"download awaiting review", 0o600, false},
		{"missing", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.mode != 0 {
				if err := os.WriteFile(filepath.Join(dir, "parsar"), []byte("binary"), tc.mode); err != nil {
					t.Fatal(err)
				}
			}
			env := map[string]any{"PATH": "/existing/tools", "OTHER": "retained"}
			addCompanionCLIPath(env, dir)
			want := "/existing/tools"
			if tc.want {
				want = dir + string(os.PathListSeparator) + want
			}
			if env["PATH"] != want || env["OTHER"] != "retained" {
				t.Fatalf("unexpected child environment: %v", env)
			}
		})
	}
}
