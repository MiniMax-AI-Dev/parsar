package cli

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/claudecode"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestCapabilityDownloadUsesPairedServerOnlyForLoopbackPG(t *testing.T) {
	base, _ := url.Parse("https://parsar.example.test/team")
	for _, tc := range []struct{ raw, want string }{
		{"http://127.0.0.1:28080/internal/blobs/pg:id?token=a%2Bb", "https://parsar.example.test/team/internal/blobs/pg:id?token=a%2Bb"},
		{"http://localhost:28080/old/internal/blobs/pg:id?token=a", "https://parsar.example.test/team/internal/blobs/pg:id?token=a"},
		{"http://[::1]:28080/internal/blobs/pg:id?token=a", "https://parsar.example.test/team/internal/blobs/pg:id?token=a"},
		{"https://bucket.example.test/skill.zip?Signature=a", "https://bucket.example.test/skill.zip?Signature=a"},
		{"http://localhost:28080/other.zip", "http://localhost:28080/other.zip"},
		{"https://public.example.test/internal/blobs/pg:id?token=a", "https://public.example.test/internal/blobs/pg:id?token=a"},
		{"file:///internal/blobs/pg:id", "file:///internal/blobs/pg:id"},
	} {
		if got := capabilityDownloadURL(tc.raw, base); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.raw, got, tc.want)
		}
	}
}

func TestCapabilityDownloadsInstallZIPThroughPairedServer(t *testing.T) {
	var archive bytes.Buffer
	w := zip.NewWriter(&archive)
	for name, content := range map[string]string{
		"SKILL.md":             "---\nname: qa-download\ndescription: Test downloaded context\n---\nRead references/policy.md.\n",
		"references/policy.md": "Collect the laptop on the third working day.\n",
	} {
		file, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/team/internal/blobs/pg:test" || r.URL.RawQuery != "token=synthetic%2Bsignature" {
			t.Errorf("download request changed signed resource: %s", r.URL)
			http.Error(w, "unexpected request", http.StatusForbidden)
			return
		}
		_, _ = w.Write(archive.Bytes())
	}))
	defer server.Close()
	root := t.TempDir()
	rawURL := "http://127.0.0.1:1/internal/blobs/pg:test?token=synthetic%2Bsignature"
	descriptor := map[string]any{"name": "qa-download", "version": "1.0.0", "download_url": rawURL, "sha256": fmt.Sprintf("%x", sha256.Sum256(archive.Bytes()))}
	opts := map[string]any{"skills": []any{descriptor}, "plugins": []any{descriptor}, "model": "preserved"}
	factory := withCapabilityDownloads(func(ctx context.Context, req proto.PromptRequestPayload, out chan<- proto.Envelope) (agent.Session, error) {
		if req.RunID != "run-test" || req.AgentOptions["model"] != "preserved" {
			t.Fatal("unrelated request fields changed")
		}
		plugin := req.AgentOptions["plugins"].([]any)[0].(map[string]any)
		skill := req.AgentOptions["skills"].([]any)[0].(map[string]any)
		if plugin["download_url"] != skill["download_url"] {
			t.Fatal("plugin and skill routing differs")
		}
		result, err := claudecode.InstallManagedSkills(ctx, nil, root, req.AgentOptions["skills"])
		if err == nil && len(result.Warnings) > 0 {
			err = fmt.Errorf("installation warnings: %v", result.Warnings)
		}
		return nil, err
	}, server.URL+"/team")
	if _, err := factory(context.Background(), proto.PromptRequestPayload{RunID: "run-test", AgentOptions: opts}, nil); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(root, "qa-download", "references", "policy.md"))
	if err != nil || string(content) != "Collect the laptop on the third working day.\n" {
		t.Fatalf("installed reference: %q, %v", content, err)
	}
	if descriptor["download_url"] != rawURL {
		t.Fatal("rewrote the original request options")
	}
}
