package dev

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/parser"
)

func zipSkillDirectory(ctx context.Context, skillDir string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !containsSkillMD(skillDir) {
		return nil, fmt.Errorf("skill directory %q is missing SKILL.md", skillDir)
	}
	buf := skillZipBuffer{ctx: ctx}
	zw := zip.NewWriter(&buf)
	entryCount := 0
	err := filepath.WalkDir(skillDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel, err := filepath.Rel(skillDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." || rel == "" {
			return nil
		}
		entryCount++
		if entryCount > parser.MaxSkillZipEntries {
			return fmt.Errorf("%w: skill exceeds %d files", parser.ErrInvalidSkillZip, parser.MaxSkillZipEntries)
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		entry, err := zw.Create(rel)
		if err != nil {
			return err
		}
		_, err = io.Copy(entry, skillContextReader{ctx: ctx, reader: file})
		return err
	})
	if closeErr := zw.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, fmt.Errorf("could not package skill directory: %w", err)
	}
	return buf.Bytes(), nil
}

// Enforce the parser's compressed-size cap while producing the archive.
type skillZipBuffer struct {
	bytes.Buffer
	ctx context.Context
}

func (b *skillZipBuffer) Write(p []byte) (int, error) {
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	if len(p) > parser.MaxSkillZipBytes-b.Len() {
		return 0, fmt.Errorf("%w: skill zip exceeds %d bytes", parser.ErrInvalidSkillZip, parser.MaxSkillZipBytes)
	}
	return b.Buffer.Write(p)
}

// Hide os.File.WriteTo so io.Copy checks cancellation between reads.
type skillContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r skillContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
