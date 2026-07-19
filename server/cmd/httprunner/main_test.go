package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteStatusUsesOverridePath(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "status.json")
	t.Setenv("PARSAR_HTTP_RUNNER_STATUS", target)

	if err := writeStatus([]byte(`{"ok":true}`)); err != nil {
		t.Fatalf("writeStatus: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.HasSuffix(string(got), "\n") {
		t.Fatal("status file should end with a newline")
	}
	if !strings.Contains(string(got), `"ok":true`) {
		t.Fatalf("status file missing payload: %q", string(got))
	}
}

func TestWriteStatusDefaultsUnderHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("PARSAR_HTTP_RUNNER_STATUS", "")

	if err := writeStatus([]byte(`{"k":"v"}`)); err != nil {
		t.Fatalf("writeStatus: %v", err)
	}

	want := filepath.Join(dir, ".parsar", "state", "http-runner-status.json")
	got, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("read default path %s: %v", want, err)
	}
	if !strings.Contains(string(got), `"k":"v"`) {
		t.Fatalf("unexpected content: %q", string(got))
	}
}

func TestWriteStatusCreatesMissingDirs(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "nested", "deeper", "status.json")
	t.Setenv("PARSAR_HTTP_RUNNER_STATUS", target)

	if err := writeStatus([]byte(`{}`)); err != nil {
		t.Fatalf("writeStatus: %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("expected file at %s: %v", target, err)
	}
}
