package pi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestInstallSkillsConcurrentSameRoot(t *testing.T) {
	const content = "Complete skill contents.\n"
	body := buildZipBytes(t, []zipFile{{Name: "SKILL.md", Body: content}})
	started, release := make(chan struct{}, 8), make(chan struct{})
	var downloads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downloads.Add(1)
		started <- struct{}{}
		select {
		case <-release:
			_, _ = w.Write(body)
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	root := t.TempDir()
	skills := []skillDescriptor{{Name: "fixture", Version: "1.0.0", DownloadURL: srv.URL, SHA256: sha256Hex(body)}}
	install := func(ctx context.Context) error {
		result, err := installSkills(ctx, discardLogger(), root, skills)
		if err != nil {
			return err
		}
		if len(result.SkillDirs) != 1 || len(result.Warnings) != 0 {
			return fmt.Errorf("dirs=%v warnings=%v", result.SkillDirs, result.Warnings)
		}
		data, err := os.ReadFile(filepath.Join(result.SkillDirs[0], "SKILL.md"))
		if err != nil {
			return err
		}
		if string(data) != content {
			return fmt.Errorf("incomplete skill: %q", data)
		}
		return nil
	}
	results := make(chan error, 8)
	go func() { results <- install(ctx) }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("first download did not start")
	}
	for range 7 {
		go func() { results <- install(ctx) }()
	}
	waiting, cancelWait := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancelWait()
	if err := install(waiting); err != context.DeadlineExceeded {
		t.Errorf("waiting install error=%v, want deadline exceeded", err)
	}
	close(release)
	for range 8 {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	if err := install(ctx); err != nil {
		t.Errorf("subsequent cached install: %v", err)
	}
	if downloads.Load() != 1 {
		t.Errorf("downloads=%d, want one cached installation", downloads.Load())
	}
}
