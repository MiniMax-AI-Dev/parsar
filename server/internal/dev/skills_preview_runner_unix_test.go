//go:build unix

package dev

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSkillPreviewRunnerScopesTemporaryFiles(t *testing.T) {
	dir := t.TempDir()
	out, err := (defaultSkillPreviewRunner{}).Run(context.Background(), dir, "sh", "-c", `printf '%s\n' "$TMPDIR" "$TMP" "$TEMP"`)
	if err != nil || string(out) != strings.Repeat(dir+"\n", 3) {
		t.Fatalf("temporary directories = %q, err = %v", out, err)
	}
}

func TestSkillPreviewRunnerCancelsDescendants(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := (defaultSkillPreviewRunner{}).Run(ctx, dir, "sh", "-c", `(sleep 2; printf leaked > leaked) & printf ready > ready; wait`)
		done <- err
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "ready")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("preview child did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled preview returned success")
		}
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("preview kept waiting on a child output pipe")
	}
	// A child that survived cancellation would still write into the directory.
	time.Sleep(2100 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(dir, "leaked")); !os.IsNotExist(err) {
		t.Fatalf("preview child outlived cancellation: %v", err)
	}
}
