package parser

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
)

func TestParsePlugin_HappyPathZipUpload(t *testing.T) {
	t.Parallel()
	buf := buildPluginZip(t, validPluginFiles())
	expectedDigest := sha256.Sum256(buf)
	wantSHA := hex.EncodeToString(expectedDigest[:])

	spec, res, err := ParsePlugin(buf, PluginSource{
		OssKey:       "capabilities/plugins/u/my-plugin.zip",
		UploadSource: canonical.UploadSourceZip,
	})
	if err != nil {
		t.Fatalf("ParsePlugin: %v; validation=%+v", err, res)
	}
	if !res.Valid {
		t.Fatalf("expected valid=true; errors=%v", res.Errors)
	}
	if spec.Kind != canonical.KindPlugin {
		t.Fatalf("spec.Kind = %q", spec.Kind)
	}
	if spec.Plugin == nil {
		t.Fatal("spec.Plugin is nil")
	}
	if spec.Plugin.Name != "my-plugin" {
		t.Fatalf("name = %q", spec.Plugin.Name)
	}
	if spec.Plugin.SHA256 != wantSHA {
		t.Fatalf("sha256 = %q, want %q", spec.Plugin.SHA256, wantSHA)
	}
	if spec.Plugin.OssKey != "capabilities/plugins/u/my-plugin.zip" {
		t.Fatalf("oss_key = %q", spec.Plugin.OssKey)
	}
	if spec.Plugin.UploadSource != canonical.UploadSourceZip {
		t.Fatalf("upload_source = %q", spec.Plugin.UploadSource)
	}
	// Author copied from plugin.json author.name.
	if spec.Plugin.Author != "Alice" {
		t.Fatalf("author = %q", spec.Plugin.Author)
	}
}

func TestParsePlugin_GitHubSourceCarriesProvenance(t *testing.T) {
	t.Parallel()
	buf := buildPluginZip(t, validPluginFiles())
	spec, _, err := ParsePlugin(buf, PluginSource{
		OssKey:       "capabilities/plugins/u/my-plugin.zip",
		UploadSource: canonical.UploadSourceGitHub,
		GitHubRepo:   "anthropics/example",
		GitHubRef:    "main",
		GitHubPath:   "plugins/foo",
	})
	if err != nil {
		t.Fatalf("ParsePlugin: %v", err)
	}
	if spec.Plugin.GitHubRepo != "anthropics/example" {
		t.Fatalf("github_repo = %q", spec.Plugin.GitHubRepo)
	}
	if spec.Plugin.GitHubRef != "main" {
		t.Fatalf("github_ref = %q", spec.Plugin.GitHubRef)
	}
	if spec.Plugin.GitHubPath != "plugins/foo" {
		t.Fatalf("github_path = %q", spec.Plugin.GitHubPath)
	}
}

func TestParsePlugin_FailsWhenValidatorRejects(t *testing.T) {
	t.Parallel()
	// Zip with no manifest — validator returns valid=false. ParsePlugin
	// must wrap the failure in ErrPluginValidationFailed and pass the
	// full result back so the handler can render errors.
	buf := buildPluginZip(t, []pluginZipFile{
		{Name: "README.md", Contents: "no manifest"},
	})
	_, res, err := ParsePlugin(buf, PluginSource{
		OssKey:       "x",
		UploadSource: canonical.UploadSourceZip,
	})
	if !errors.Is(err, ErrPluginValidationFailed) {
		t.Fatalf("err = %v, want ErrPluginValidationFailed", err)
	}
	if res == nil || res.Valid {
		t.Fatalf("expected non-nil result with valid=false; got %+v", res)
	}
	if !strings.Contains(err.Error(), "missing .claude-plugin/plugin.json") {
		t.Fatalf("err = %v, want manifest-missing hint", err)
	}
}

func TestParsePlugin_FillsVersionDefault(t *testing.T) {
	t.Parallel()
	// plugin.json with no version field — parser should default to
	// 0.0.0 and add a warning so the operator can fix the upstream.
	buf := buildPluginZip(t, []pluginZipFile{
		{Name: ".claude-plugin/plugin.json", Contents: `{"name":"my-plugin"}`},
	})
	spec, res, err := ParsePlugin(buf, PluginSource{
		OssKey:       "x",
		UploadSource: canonical.UploadSourceZip,
	})
	if err != nil {
		t.Fatalf("ParsePlugin: %v", err)
	}
	if spec.Plugin.Version != "0.0.0" {
		t.Fatalf("version = %q, want 0.0.0", spec.Plugin.Version)
	}
	if !containsString(res.Warnings, `defaulted to "0.0.0"`) {
		t.Fatalf("expected version-default warning; warnings=%v", res.Warnings)
	}
}

func TestParsePlugin_RejectsEmptyOssKey(t *testing.T) {
	t.Parallel()
	buf := buildPluginZip(t, validPluginFiles())
	_, _, err := ParsePlugin(buf, PluginSource{
		UploadSource: canonical.UploadSourceZip,
	})
	if err == nil {
		t.Fatal("expected error on empty OssKey")
	}
	if !strings.Contains(err.Error(), "OssKey is required") {
		t.Fatalf("err = %v, want OssKey-required hint", err)
	}
}

func TestParsePlugin_RejectsUnknownUploadSource(t *testing.T) {
	t.Parallel()
	buf := buildPluginZip(t, validPluginFiles())
	_, _, err := ParsePlugin(buf, PluginSource{
		OssKey:       "x",
		UploadSource: "magic",
	})
	if err == nil {
		t.Fatal("expected error on unknown UploadSource")
	}
}

func TestParsePlugin_SpecRoundTripsViaCanonical(t *testing.T) {
	t.Parallel()
	// Sanity: the spec we built should pass canonical.Spec.Validate
	// (already implicit in ParsePlugin) AND round-trip through JSON
	// without losing the plugin body.
	buf := buildPluginZip(t, validPluginFiles())
	spec, _, err := ParsePlugin(buf, PluginSource{
		OssKey:       "capabilities/plugins/u/my-plugin.zip",
		UploadSource: canonical.UploadSourceZip,
	})
	if err != nil {
		t.Fatalf("ParsePlugin: %v", err)
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("spec.Validate: %v", err)
	}
}
