package testutil

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// CheckFunc is an adapter CLI availability check.
type CheckFunc func(context.Context, string) (string, error)

// Contract describes the shared behavior expected from a CLI version probe.
type Contract struct {
	Name               string
	DefaultBinary      string
	MissingError       error
	Check              CheckFunc
	WhitespaceDefaults bool
}

// RunContract verifies an adapter's shared CLI version probe behavior.
func RunContract(t *testing.T, contract Contract) {
	t.Helper()

	t.Run("missing binary wraps exported sentinel", func(t *testing.T) {
		binary := "parsar-daemon-nonexistent-" + contract.Name + "-stub"
		_, err := contract.Check(context.Background(), binary)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !errors.Is(err, contract.MissingError) {
			t.Fatalf("error %v does not wrap %v", err, contract.MissingError)
		}
		want := fmt.Sprintf("%s: %s", contract.MissingError, binary)
		if err.Error() != want {
			t.Fatalf("error = %q, want %q", err, want)
		}
	})

	t.Run("empty binary uses adapter default", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		_, err := contract.Check(context.Background(), "")
		want := fmt.Sprintf("%s: %s", contract.MissingError, contract.DefaultBinary)
		if err == nil || err.Error() != want {
			t.Fatalf("error = %v, want %q", err, want)
		}
	})

	t.Run("whitespace binary preserves adapter behavior", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		_, err := contract.Check(context.Background(), "   ")
		binary := "   "
		if contract.WhitespaceDefaults {
			binary = contract.DefaultBinary
		}
		want := fmt.Sprintf("%s: %s", contract.MissingError, binary)
		if err == nil || err.Error() != want {
			t.Fatalf("error = %v, want %q", err, want)
		}
	})

	if runtime.GOOS == "windows" {
		return
	}

	t.Run("returns trimmed first stdout line", func(t *testing.T) {
		stub := writeStub(t, "fake-"+contract.Name, "#!/bin/sh\nprintf '  "+contract.Name+" 9.9.9\\nextra line  \\n'\n")
		version, err := contract.Check(context.Background(), stub)
		if err != nil {
			t.Fatalf("check CLI: %v", err)
		}
		want := contract.Name + " 9.9.9"
		if version != want {
			t.Fatalf("version = %q, want %q", version, want)
		}
	})

	t.Run("surfaces adapter-specific nonzero error", func(t *testing.T) {
		stub := writeStub(t, "broken-"+contract.Name, "#!/bin/sh\necho 'kaboom' 1>&2\nexit 17\n")
		_, err := contract.Check(context.Background(), stub)
		want := contract.Name + " --version failed: kaboom"
		if err == nil || err.Error() != want {
			t.Fatalf("error = %v, want %q", err, want)
		}
		if errors.Is(err, contract.MissingError) {
			t.Fatalf("broken present binary wraps missing sentinel: %v", err)
		}
	})

	t.Run("rejects empty output with adapter-specific error", func(t *testing.T) {
		stub := writeStub(t, "silent-"+contract.Name, "#!/bin/sh\nexit 0\n")
		_, err := contract.Check(context.Background(), stub)
		want := contract.Name + " --version returned empty output"
		if err == nil || err.Error() != want {
			t.Fatalf("error = %v, want %q", err, want)
		}
	})
}

// WriteStub creates a POSIX executable for adapter-specific tests.
func WriteStub(t *testing.T, name, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-only stub script; daemon does not target Windows")
	}
	return writeStub(t, name, body)
}

func writeStub(t *testing.T, name, body string) string {
	t.Helper()
	stub := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(stub, []byte(body), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return stub
}
