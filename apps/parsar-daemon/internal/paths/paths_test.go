package paths_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/paths"
)

// withTempHome points PARSAR_HOME at a fresh tempdir for the test.
// t.Setenv refuses to run with t.Parallel — the exact constraint
// we want.
func withTempHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PARSAR_HOME", dir)
	return dir
}

func TestValidateProfile(t *testing.T) {
	good := []string{"default", "test", "prod", "alpha-1", "alice_mac", "a.b.c", strings.Repeat("a", 64)}
	for _, name := range good {
		if err := paths.ValidateProfile(name); err != nil {
			t.Errorf("ValidateProfile(%q) returned %v, want nil", name, err)
		}
	}
	bad := []string{
		"",
		"../escape",
		"with/slash",
		"with\\backslash",
		"with space",
		strings.Repeat("a", 65),
		"emoji_\xf0\x9f\x98\x80",
	}
	for _, name := range bad {
		if err := paths.ValidateProfile(name); err == nil {
			t.Errorf("ValidateProfile(%q) returned nil, want error", name)
		}
	}
}

func TestRootHonoursParsarHome(t *testing.T) {
	home := withTempHome(t)
	got, err := paths.Root()
	if err != nil {
		t.Fatalf("Root: %v", err)
	}
	if got != home {
		t.Fatalf("Root = %q, want %q", got, home)
	}
}

func TestProfileDirAndFiles(t *testing.T) {
	home := withTempHome(t)
	want := filepath.Join(home, "parsar-daemon", "test")

	gotDir, err := paths.ProfileDir("test")
	if err != nil {
		t.Fatalf("ProfileDir: %v", err)
	}
	if gotDir != want {
		t.Fatalf("ProfileDir = %q, want %q", gotDir, want)
	}

	// ProfileDir alone must not create the directory.
	if _, err := os.Stat(gotDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ProfileDir created dir prematurely: stat err = %v", err)
	}

	cases := map[string]func(string) (string, error){
		"auth.json":     paths.AuthFile,
		"connect.pid":   paths.PIDFile,
		"connect.log":   paths.LogFile,
		"sessions.json": paths.SessionsFile,
	}
	for filename, fn := range cases {
		got, err := fn("test")
		if err != nil {
			t.Fatalf("%s resolver: %v", filename, err)
		}
		expect := filepath.Join(want, filename)
		if got != expect {
			t.Errorf("%s = %q, want %q", filename, got, expect)
		}
	}
}

func TestEnsureProfileDirCreates0700(t *testing.T) {
	_ = withTempHome(t)
	dir, err := paths.EnsureProfileDir("default")
	if err != nil {
		t.Fatalf("EnsureProfileDir: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat after EnsureProfileDir: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("EnsureProfileDir returned %q which is not a directory", dir)
	}
	if mode := info.Mode().Perm(); mode != 0o700 {
		t.Errorf("EnsureProfileDir mode = %o, want 0700", mode)
	}

	// Idempotent (mkdir -p semantics).
	dir2, err := paths.EnsureProfileDir("default")
	if err != nil {
		t.Fatalf("EnsureProfileDir (second call): %v", err)
	}
	if dir2 != dir {
		t.Fatalf("EnsureProfileDir second call returned %q, want %q", dir2, dir)
	}
}

func TestInvalidProfileShortCircuits(t *testing.T) {
	_ = withTempHome(t)
	if _, err := paths.ProfileDir("bad/profile"); err == nil {
		t.Fatal("ProfileDir accepted invalid profile name")
	}
	if _, err := paths.AuthFile("bad/profile"); err == nil {
		t.Fatal("AuthFile accepted invalid profile name")
	}
	if _, err := paths.EnsureProfileDir("bad/profile"); err == nil {
		t.Fatal("EnsureProfileDir accepted invalid profile name")
	}
}
