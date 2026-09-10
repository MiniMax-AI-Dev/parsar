package dev

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/parser"
)

func TestSkillPackageRejectsOversize(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: example\ndescription: Example\n---\nInstructions."), 0o600); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, parser.MaxSkillZipBytes+1)
	if _, err := rand.Read(data); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "large.bin"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := zipSkillDirectory(context.Background(), dir)
	if err == nil || out != nil {
		t.Fatalf("oversize archive returned %d bytes, err = %v", len(out), err)
	}
}

func TestSkillPackageRejectsTooManyFiles(t *testing.T) {
	dir := t.TempDir()
	for i := range parser.MaxSkillZipEntries {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file-%d", i)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("Instructions"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := zipSkillDirectory(context.Background(), dir)
	if !errors.Is(err, parser.ErrInvalidSkillZip) || out != nil {
		t.Fatalf("too many files: %d bytes, %v", len(out), err)
	}
}

func TestSkillPackageRejectsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out, err := zipSkillDirectory(ctx, t.TempDir())
	if !errors.Is(err, context.Canceled) || out != nil {
		t.Fatalf("cancelled package: %d bytes, %v", len(out), err)
	}
}

func TestSkillPackageCopyStopsBetweenReads(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := &cancellingSkillReader{cancel: cancel}
	_, err := io.Copy(io.Discard, skillContextReader{ctx: ctx, reader: source})
	if !errors.Is(err, context.Canceled) || source.reads != 1 {
		t.Fatalf("copy after cancellation: reads = %d, err = %v", source.reads, err)
	}
}

type cancellingSkillReader struct {
	cancel context.CancelFunc
	reads  int
}

func (r *cancellingSkillReader) Read(p []byte) (int, error) {
	r.reads++
	r.cancel()
	return len(p), nil
}
