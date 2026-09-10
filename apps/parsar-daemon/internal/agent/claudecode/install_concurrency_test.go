package claudecode

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

func installConcurrentFixture(ctx context.Context, kind, root, url string, body []byte) error {
	var dirs, warnings []string
	var err error
	if kind == "plugin" {
		var result PluginInstallResult
		result, err = installPlugins(ctx, discardLogger(), root, []pluginDescriptor{
			{Name: "fixture", Version: "1.0.0", DownloadURL: url, SHA256: sha256Hex(body)},
		})
		dirs, warnings = result.PluginDirs, result.Warnings
	} else {
		var result SkillInstallResult
		if kind == "managed" {
			result, err = InstallManagedSkills(ctx, discardLogger(), root, []any{map[string]any{
				"name": "fixture", "version": "1.0.0", "download_url": url, "sha256": sha256Hex(body),
			}})
		} else {
			result, err = installSkillsAtRoot(ctx, discardLogger(), root, []skillDescriptor{
				{Name: "fixture", Version: "1.0.0", DownloadURL: url, SHA256: sha256Hex(body)},
			}, "skills")
		}
		dirs, warnings = result.SkillDirs, result.Warnings
	}
	if err != nil {
		return err
	}
	if len(dirs) != 1 || len(warnings) != 0 {
		return fmt.Errorf("dirs=%v warnings=%v", dirs, warnings)
	}
	content, err := os.ReadFile(filepath.Join(dirs[0], "SKILL.md"))
	if err != nil {
		return err
	}
	if string(content) != "Complete fixture contents.\n" {
		return fmt.Errorf("incomplete file: %q", content)
	}
	return nil
}

func TestInstallsConcurrentSameRoot(t *testing.T) {
	for _, kind := range []string{"plugin", "skill", "managed"} {
		t.Run(kind, func(t *testing.T) {
			body := buildPluginZipBytes(t, []pluginZipFile{{Name: "SKILL.md", Body: "Complete fixture contents.\n"}})
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = w.Write(body)
			}))
			defer srv.Close()
			root := t.TempDir()
			results := make(chan error, 8)
			for range 8 {
				go func() { results <- installConcurrentFixture(context.Background(), kind, root, srv.URL, body) }()
			}
			for range 8 {
				if err := <-results; err != nil {
					t.Error(err)
				}
			}
			if calls.Load() != 1 {
				t.Errorf("downloads=%d, want one installation shared through the cache", calls.Load())
			}
		})
	}
}

func TestInstallWaitCancellation(t *testing.T) {
	for _, kind := range []string{"plugin", "skill", "managed"} {
		t.Run(kind, func(t *testing.T) {
			body := buildPluginZipBytes(t, []pluginZipFile{{Name: "SKILL.md", Body: "Complete fixture contents.\n"}})
			started, release := make(chan struct{}, 2), make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			first := make(chan error, 1)
			go func() { first <- installConcurrentFixture(ctx, kind, root, srv.URL, body) }()
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("first download did not start")
			}
			waiting, cancelWait := context.WithCancel(ctx)
			cancelWait()
			if err := installConcurrentFixture(waiting, kind, root, srv.URL, body); err != context.Canceled {
				t.Errorf("waiting install error=%v, want cancellation", err)
			}
			close(release)
			if err := <-first; err != nil {
				t.Fatal(err)
			}
			if err := installConcurrentFixture(ctx, kind, root, srv.URL, body); err != nil {
				t.Fatalf("cancelled waiter damaged installation: %v", err)
			}
			if len(started) != 0 {
				t.Fatal("cancelled waiter started another download")
			}
		})
	}
}

func TestInstallsIndependentRootsRemainConcurrent(t *testing.T) {
	body := buildPluginZipBytes(t, []pluginZipFile{{Name: "SKILL.md", Body: "Complete fixture contents.\n"}})
	started, release := make(chan struct{}, 2), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	results := make(chan error, 2)
	for _, root := range []string{t.TempDir(), t.TempDir()} {
		go func() { results <- installConcurrentFixture(ctx, "plugin", root, srv.URL, body) }()
	}
	for range 2 {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("independent roots were serialized")
		}
	}
	close(release)
	for range 2 {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
}
