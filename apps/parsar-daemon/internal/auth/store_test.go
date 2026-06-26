package auth_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/auth"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/paths"
)

func withTempHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PARSAR_HOME", dir)
	return dir
}

func TestSaveLoadRoundTrip(t *testing.T) {
	_ = withTempHome(t)
	now := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	want := auth.Profile{
		ServerURL:        "https://parsar.example.com",
		RuntimeID:        "rt_abc123",
		RunnerCredential: "secret-credential",
		DeviceName:       "alice-mac",
		Hostname:         "alice-mac.local",
		PairedAt:         now,
	}
	if err := auth.Save("test", want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := auth.Load("test")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.ServerURL != want.ServerURL ||
		got.RuntimeID != want.RuntimeID ||
		got.RunnerCredential != want.RunnerCredential ||
		got.DeviceName != want.DeviceName ||
		got.Hostname != want.Hostname ||
		!got.PairedAt.Equal(want.PairedAt) {
		t.Fatalf("Load round-trip mismatch:\n got=%+v\nwant=%+v", got, want)
	}
}

func TestSaveSetsRestrictivePerms(t *testing.T) {
	_ = withTempHome(t)
	if err := auth.Save("default", auth.Profile{ServerURL: "https://x", RuntimeID: "rt", RunnerCredential: "c"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	authPath, err := paths.AuthFile("default")
	if err != nil {
		t.Fatalf("AuthFile: %v", err)
	}
	info, err := os.Stat(authPath)
	if err != nil {
		t.Fatalf("stat auth.json: %v", err)
	}
	// auth.json holds the long-lived runner_credential — anything
	// but 0600 leaks it on a shared CI box.
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("auth.json perm = %o, want 0600", mode)
	}
}

func TestSaveIsAtomicNoStrayTempFile(t *testing.T) {
	_ = withTempHome(t)
	if err := auth.Save("default", auth.Profile{ServerURL: "https://x", RuntimeID: "rt", RunnerCredential: "c"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	authPath, err := paths.AuthFile("default")
	if err != nil {
		t.Fatalf("AuthFile: %v", err)
	}
	// Writes to auth.json.tmp then renames — no stray .tmp on success.
	entries, err := os.ReadDir(filepath.Dir(authPath))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Fatalf("found stray temp file after Save: %s", e.Name())
		}
	}
}

func TestSaveOverwritesAndHealsPerms(t *testing.T) {
	_ = withTempHome(t)
	if err := auth.Save("default", auth.Profile{ServerURL: "https://x", RuntimeID: "rt1", RunnerCredential: "c1"}); err != nil {
		t.Fatalf("Save first: %v", err)
	}
	authPath, err := paths.AuthFile("default")
	if err != nil {
		t.Fatalf("AuthFile: %v", err)
	}
	// Simulate a previously-world-readable file (user chmod'd it);
	// atomic-rename Save must re-establish 0600 on the new inode.
	if err := os.Chmod(authPath, 0o644); err != nil {
		t.Fatalf("chmod loose perms: %v", err)
	}
	if err := auth.Save("default", auth.Profile{ServerURL: "https://x", RuntimeID: "rt2", RunnerCredential: "c2"}); err != nil {
		t.Fatalf("Save second: %v", err)
	}
	info, err := os.Stat(authPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("perm after re-save = %o, want 0600 (healing failed)", mode)
	}
	got, err := auth.Load("default")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.RuntimeID != "rt2" || got.RunnerCredential != "c2" {
		t.Errorf("overwrite did not take effect: %+v", got)
	}
}

func TestLoadMissingReturnsErrNotPaired(t *testing.T) {
	_ = withTempHome(t)
	_, err := auth.Load("default")
	if !errors.Is(err, auth.ErrNotPaired) {
		t.Fatalf("Load on missing profile returned %v, want ErrNotPaired", err)
	}
	// ErrNotPaired must also wrap fs.ErrNotExist for the canonical
	// "missing file" check.
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("ErrNotPaired must wrap fs.ErrNotExist, got %v", err)
	}
}

func TestLoadCorruptJSONReturnsError(t *testing.T) {
	_ = withTempHome(t)
	dir, err := paths.EnsureProfileDir("default")
	if err != nil {
		t.Fatalf("EnsureProfileDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte("{not valid json"), 0o600); err != nil {
		t.Fatalf("seed corrupt file: %v", err)
	}
	_, err = auth.Load("default")
	if err == nil {
		t.Fatal("Load returned nil error on corrupt JSON")
	}
	if errors.Is(err, auth.ErrNotPaired) {
		t.Fatalf("Load on corrupt JSON should not be ErrNotPaired: %v", err)
	}
}

func TestDeleteIsIdempotent(t *testing.T) {
	_ = withTempHome(t)
	if err := auth.Delete("default"); err != nil {
		t.Fatalf("Delete on missing profile returned %v, want nil (idempotent)", err)
	}
	if err := auth.Save("default", auth.Profile{ServerURL: "https://x", RuntimeID: "rt", RunnerCredential: "c"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := auth.Delete("default"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := auth.Load("default"); !errors.Is(err, auth.ErrNotPaired) {
		t.Fatalf("Load after Delete = %v, want ErrNotPaired", err)
	}
	// Second Delete must still succeed.
	if err := auth.Delete("default"); err != nil {
		t.Fatalf("Delete second call = %v, want nil", err)
	}
}

func TestSaveRejectsEmptyProfile(t *testing.T) {
	_ = withTempHome(t)
	if err := auth.Save("", auth.Profile{ServerURL: "https://x", RuntimeID: "rt", RunnerCredential: "c"}); err == nil {
		t.Fatal("Save with empty profile should error")
	}
}
