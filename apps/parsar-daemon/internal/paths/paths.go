// Package paths resolves on-disk locations for parsar-daemon state under
// ~/.parsar/parsar-daemon/<profile>/ — one subdir per profile so "test"
// and "prod" servers can be paired in parallel without colliding.
//
// Files are 0o600, parent dir 0o700. These functions only resolve
// paths — callers do the I/O.
package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

const DefaultProfile = "default"

// profilePattern restricts profile names to filesystem-safe chars so
// a malicious --profile can't escape via "../etc/passwd" tricks.
var profilePattern = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,64}$`)

// ValidateProfile rejects names that wouldn't survive being used as
// a directory component.
func ValidateProfile(name string) error {
	if name == "" {
		return fmt.Errorf("profile name must not be empty")
	}
	if !profilePattern.MatchString(name) {
		return fmt.Errorf("profile %q must match %s", name, profilePattern.String())
	}
	return nil
}

// Root returns ~/.parsar. Honours PARSAR_HOME for tests /
// sandbox environments without a writable home.
func Root() (string, error) {
	if override := os.Getenv("PARSAR_HOME"); override != "" {
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".parsar"), nil
}

// ProfileDir returns ~/.parsar/parsar-daemon/<profile>. NOT created;
// use EnsureProfileDir.
func ProfileDir(profile string) (string, error) {
	if err := ValidateProfile(profile); err != nil {
		return "", err
	}
	root, err := Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "parsar-daemon", profile), nil
}

// EnsureProfileDir mkdirs the profile dir at mode 0o700 and returns
// its path. Idempotent.
func EnsureProfileDir(profile string) (string, error) {
	dir, err := ProfileDir(profile)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create profile dir %s: %w", dir, err)
	}
	return dir, nil
}

// AuthFile returns the absolute path to auth.json for a profile.
func AuthFile(profile string) (string, error) {
	dir, err := ProfileDir(profile)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "auth.json"), nil
}

// PIDFile returns the absolute path to connect.pid for a profile.
func PIDFile(profile string) (string, error) {
	dir, err := ProfileDir(profile)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "connect.pid"), nil
}

// LogFile returns the absolute path to connect.log for a profile.
func LogFile(profile string) (string, error) {
	dir, err := ProfileDir(profile)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "connect.log"), nil
}

// SessionsFile returns the absolute path to sessions.json. Used by
// claudecode to persist conversation → claude_session_id for
// --resume.
func SessionsFile(profile string) (string, error) {
	dir, err := ProfileDir(profile)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "sessions.json"), nil
}
