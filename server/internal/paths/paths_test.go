package paths

import "testing"

func TestMustBeUserPathRejectsRelativePath(t *testing.T) {
	if _, err := MustBeUserPath("relative/path"); err == nil {
		t.Fatal("expected relative path to be rejected")
	}
}

func TestRootUsesHomeParsar(t *testing.T) {
	root, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	if root == "" || root == ".parsar" {
		t.Fatalf("unexpected root: %q", root)
	}
}
