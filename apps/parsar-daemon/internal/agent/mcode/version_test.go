package mcode

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestOnlyQualifiedLatestCLIIsAvailable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX CLI fixture")
	}
	for _, version := range []string{SupportedVersion, "0.3.11", "0.4.13"} {
		t.Run(version, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "mcode")
			if err := os.WriteFile(binary, []byte("#!/bin/sh\necho "+version+"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			got, err := CheckCLIAvailable(t.Context(), binary)
			if got != version || (err == nil) != (version == SupportedVersion) {
				t.Fatalf("version %q, error %v", got, err)
			}
		})
	}
}
