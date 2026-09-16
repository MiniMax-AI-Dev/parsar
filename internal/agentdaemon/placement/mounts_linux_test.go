//go:build linux

package placement

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
)

func TestAmbiguousHostMountsCannotAuthorizeEnrollment(t *testing.T) {
	for _, scenario := range []string{"directory-bind", "whole-filesystem-bind", "nested-mount", "stacked-mount", "missing-evidence"} {
		t.Run(scenario, func(t *testing.T) {
			f := newFixture(t)
			info := "1 0 8:2 / / rw - ext4 /dev/test rw\n"
			switch scenario {
			case "directory-bind":
				info += fmt.Sprintf("2 1 8:2 /home/operator %s rw - ext4 /dev/test rw\n", f.workspace)
			case "whole-filesystem-bind":
				info += fmt.Sprintf("2 1 8:2 / %s rw - ext4 /dev/test rw\n", f.workspace)
			case "nested-mount":
				info += fmt.Sprintf("2 1 0:8 / %s/secret rw - tmpfs tmpfs rw\n", f.workspace)
			case "stacked-mount":
				info += fmt.Sprintf("2 1 0:8 / %s rw - tmpfs tmpfs rw\n3 1 0:9 / %s rw - tmpfs tmpfs rw\n", f.workspace, f.workspace)
			case "missing-evidence":
				info = ""
			}
			writeTestFile(t, filepath.Join(f.c.procRoot, "self/mountinfo"), info)
			if _, err := f.c.Enroll(context.Background(), testID, "owner-1", f.workspace); err == nil {
				t.Fatal("ambiguous host mount authorized")
			}
			if f.stops+f.removals != 0 {
				t.Fatal("unqualified supervisor mutated")
			}
		})
	}
}

func TestNewHostAliasIsRejectedBeforeRetirement(t *testing.T) {
	f := newFixture(t)
	f.enroll()
	writeTestFile(t, filepath.Join(f.c.procRoot, "self/mountinfo"), "1 0 8:2 / / rw - ext4 /dev/test rw\n2 1 8:2 /home/operator /mnt/alias rw - ext4 /dev/test rw\n")
	if _, err := f.c.Retire(context.Background(), testID); err == nil {
		t.Fatal("changed host mount topology accepted")
	}
	if f.stops+f.removals != 0 {
		t.Fatal("supervisor mutated before mount qualification")
	}
}
