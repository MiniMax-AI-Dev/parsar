package render

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
)

func pluginFixture() canonical.Spec {
	return canonical.Spec{
		SchemaVersion: canonical.SchemaVersionCurrent,
		Kind:          canonical.KindPlugin,
		Plugin: &canonical.PluginSpec{
			Name:         "my-plugin",
			DisplayName:  "My Plugin",
			Version:      "1.2.3",
			Description:  "A test plugin",
			Author:       "Alice",
			OssKey:       "capabilities/plugins/abc-def/my-plugin.zip",
			SHA256:       "ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb",
			UploadSource: canonical.UploadSourceZip,
		},
	}
}

func TestClaudeCodeRenderer_Plugin_EmitsDescriptor(t *testing.T) {
	t.Parallel()
	out, err := claudeCodeRenderer{}.Render(context.Background(), pluginFixture())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var doc claudeCodePluginDocument
	if err := json.Unmarshal(out.Content, &doc); err != nil {
		t.Fatalf("decode: %v; raw=%s", err, out.Content)
	}
	if doc.Name != "my-plugin" {
		t.Fatalf("name = %q", doc.Name)
	}
	if doc.Version != "1.2.3" {
		t.Fatalf("version = %q", doc.Version)
	}
	if doc.OssKey != "capabilities/plugins/abc-def/my-plugin.zip" {
		t.Fatalf("oss_key = %q", doc.OssKey)
	}
	if doc.SHA256 != "ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb" {
		t.Fatalf("sha256 = %q", doc.SHA256)
	}
}

func TestClaudeCodeRenderer_Plugin_OmitsDownloadURL(t *testing.T) {
	t.Parallel()
	// Render is pure — minting a presigned URL is the dispatch layer's job.
	out, _ := claudeCodeRenderer{}.Render(context.Background(), pluginFixture())
	var asMap map[string]any
	if err := json.Unmarshal(out.Content, &asMap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, k := range []string{"download_url", "downloadUrl", "url"} {
		if _, ok := asMap[k]; ok {
			t.Fatalf("render output unexpectedly carries %q field: %s", k, out.Content)
		}
	}
}

func TestClaudeCodeRenderer_Plugin_RejectsInvalidSpec(t *testing.T) {
	t.Parallel()
	spec := pluginFixture()
	spec.Plugin.Name = ""
	_, err := claudeCodeRenderer{}.Render(context.Background(), spec)
	if err == nil {
		t.Fatal("expected error on invalid spec, got nil")
	}
}

func TestOpenCodeRenderer_Plugin_ReturnsUnsupported(t *testing.T) {
	t.Parallel()
	_, err := openCodeRenderer{}.Render(context.Background(), pluginFixture())
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

func TestCodexRenderer_Plugin_ReturnsUnsupported(t *testing.T) {
	t.Parallel()
	_, err := codexRenderer{}.Render(context.Background(), pluginFixture())
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

func TestFor_AllTargetsReachable(t *testing.T) {
	t.Parallel()
	for _, target := range []Target{TargetOpenCode, TargetClaudeCode, TargetCodex} {
		t.Run(string(target), func(t *testing.T) {
			t.Parallel()
			r, err := For(target)
			if err != nil {
				t.Fatalf("For(%q): %v", target, err)
			}
			if r.Target() != target {
				t.Fatalf("renderer reports Target()=%q, want %q", r.Target(), target)
			}
		})
	}
}
