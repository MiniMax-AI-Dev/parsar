// Package auth persists the credential bundle from
// /api/v1/runtimes/pair: server URL, runtime row id (= device_id), and
// the long-lived runner_credential. Stored as JSON per-profile at
// ~/.parsar/parsar-daemon/<profile>/auth.json (0o600), written via
// atomic rename so a half-flushed pair never leaves the daemon paired
// with garbage state.
package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/paths"
)

// Profile is the on-disk representation of one paired credential.
type Profile struct {
	// ServerURL is the absolute base URL the daemon dials (no
	// trailing slash). The daemon joins this with paths like
	// /agent-daemon/bootstrap.
	ServerURL string `json:"server_url"`

	// RuntimeID is the runtimes row id minted at pair time. The
	// gateway uses it verbatim as device_id on WS upgrade.
	RuntimeID string `json:"runtime_id"`

	// RunnerCredential is the bearer presented on every
	// /agent-daemon/* call. Stored plaintext in a 0o600 file; the
	// server holds only the hash, so this is the only proof of
	// identity and MUST NOT be checked into VCS.
	RunnerCredential string `json:"runner_credential"`

	DeviceName string `json:"device_name,omitempty"`

	Hostname string `json:"hostname,omitempty"`

	// PairedAt is when the credential was minted. `omitzero` because
	// `omitempty` doesn't elide zero structs like time.Time.
	PairedAt time.Time `json:"paired_at,omitzero"`

	// RunnerPublicKey is the base64 X25519 public half generated at
	// pair time. Server stores the matching value in
	// runtimes.config.runner_public_key for SealAnonymous addressed
	// to this daemon.
	RunnerPublicKey string `json:"runner_public_key,omitempty"`

	// RunnerPrivateKey is the base64 X25519 private half — used by
	// runtimecrypto.OpenSeal to decrypt incoming sealed payloads.
	// MUST NEVER appear in logs or leave the box.
	RunnerPrivateKey string `json:"runner_private_key,omitempty"`
}

// ErrNotPaired is returned by Load when no auth.json exists for the
// requested profile.
var ErrNotPaired = errors.New("auth: not paired — use `parsar-daemon connect --url ... --token ...`")

// Save writes p atomically to the profile's auth.json (0o600 even if
// the previous file was world-readable).
func Save(profile string, p Profile) error {
	if profile == "" {
		return fmt.Errorf("auth: profile name required")
	}
	dir, err := paths.EnsureProfileDir(profile)
	if err != nil {
		return err
	}
	authFile := filepath.Join(dir, "auth.json")
	tmp := authFile + ".tmp"
	raw, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("auth: marshal profile: %w", err)
	}
	// os.WriteFile with the final mode in one shot, so umask is
	// irrelevant.
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("auth: write tmp: %w", err)
	}
	if err := os.Rename(tmp, authFile); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("auth: rename: %w", err)
	}
	return nil
}

// Load reads the profile's auth.json. Returns ErrNotPaired wrapping
// fs.ErrNotExist when the file is missing.
func Load(profile string) (Profile, error) {
	authPath, err := paths.AuthFile(profile)
	if err != nil {
		return Profile{}, err
	}
	raw, err := os.ReadFile(authPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Multi-%w so errors.Is matches both ErrNotPaired AND
			// fs.ErrNotExist.
			return Profile{}, fmt.Errorf("%w (looked at %s): %w", ErrNotPaired, authPath, err)
		}
		return Profile{}, fmt.Errorf("auth: read: %w", err)
	}
	var p Profile
	if err := json.Unmarshal(raw, &p); err != nil {
		return Profile{}, fmt.Errorf("auth: parse %s: %w", authPath, err)
	}
	return p, nil
}

// Delete removes the auth.json for a profile. Idempotent — missing
// file is not an error.
func Delete(profile string) error {
	authPath, err := paths.AuthFile(profile)
	if err != nil {
		return err
	}
	if err := os.Remove(authPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("auth: delete: %w", err)
	}
	return nil
}
