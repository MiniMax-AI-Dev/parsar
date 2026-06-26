package canonical

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// validPluginSpec is a baseline tests mutate. SHA256 is a real digest of a
// 1-byte string — shape matters, the origin doesn't.
func validPluginSpec() PluginSpec {
	return PluginSpec{
		Name:         "my-plugin",
		DisplayName:  "My Plugin",
		Version:      "1.0.0",
		Description:  "A test plugin",
		Author:       "Alice",
		Keywords:     []string{"a", "b"},
		OssKey:       "capabilities/plugins/abc-def/my-plugin.zip",
		SHA256:       "ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb",
		UploadSource: UploadSourceZip,
	}
}

func TestPluginSpec_Validate_HappyPath(t *testing.T) {
	t.Parallel()
	if err := validPluginSpec().Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestPluginSpec_Validate_RejectsEmptyName(t *testing.T) {
	t.Parallel()
	p := validPluginSpec()
	p.Name = ""
	err := p.Validate()
	if !errors.Is(err, ErrInvalidPlugin) {
		t.Fatalf("err = %v, want ErrInvalidPlugin", err)
	}
	if !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("err = %v, want name-is-required hint", err)
	}
}

func TestPluginSpec_Validate_RejectsUnsafeNameChars(t *testing.T) {
	t.Parallel()
	// Only path separators, control chars, and reserved dot names are
	// rejected — everything else (Unicode, casing, spaces) is fine and
	// matches the parser's stance.
	for _, bad := range []string{"with/slash", "with\\slash", "with\x00nul", "with\ttab", ".", ".."} {
		t.Run(bad, func(t *testing.T) {
			t.Parallel()
			p := validPluginSpec()
			p.Name = bad
			err := p.Validate()
			if !errors.Is(err, ErrInvalidPlugin) {
				t.Fatalf("err = %v, want ErrInvalidPlugin for name %q", err, bad)
			}
		})
	}
}

func TestPluginSpec_Validate_AcceptsRelaxedNames(t *testing.T) {
	t.Parallel()
	for _, good := range []string{"MyPlugin", "with space", "-leading", "trailing-", "double--dash", "1leading", "面试题目生成助手", "Plug-in 中英 1.0"} {
		t.Run(good, func(t *testing.T) {
			t.Parallel()
			p := validPluginSpec()
			p.Name = good
			if err := p.Validate(); err != nil {
				t.Fatalf("err = %v, want nil for name %q", err, good)
			}
		})
	}
}

func TestPluginSpec_Validate_RejectsEmptyVersion(t *testing.T) {
	t.Parallel()
	p := validPluginSpec()
	p.Version = ""
	if err := p.Validate(); !errors.Is(err, ErrInvalidPlugin) {
		t.Fatalf("err = %v, want ErrInvalidPlugin", err)
	}
}

func TestPluginSpec_Validate_RejectsEmptyOssKey(t *testing.T) {
	t.Parallel()
	p := validPluginSpec()
	p.OssKey = ""
	err := p.Validate()
	if !errors.Is(err, ErrInvalidPlugin) {
		t.Fatalf("err = %v, want ErrInvalidPlugin", err)
	}
	if !strings.Contains(err.Error(), "oss_key") {
		t.Fatalf("err = %v, want oss_key hint", err)
	}
}

func TestPluginSpec_Validate_RejectsBadSHA256(t *testing.T) {
	t.Parallel()
	bad := []string{
		"",
		"too-short",
		strings.Repeat("g", 64), // valid length, invalid hex
		strings.Repeat("A", 64), // uppercase rejected (we want lowercase)
		strings.Repeat("a", 63), // off-by-one
		strings.Repeat("a", 65),
	}
	for _, s := range bad {
		t.Run(s, func(t *testing.T) {
			t.Parallel()
			p := validPluginSpec()
			p.SHA256 = s
			if err := p.Validate(); !errors.Is(err, ErrInvalidPlugin) {
				t.Fatalf("err = %v for sha256 %q, want ErrInvalidPlugin", err, s)
			}
		})
	}
}

func TestPluginSpec_Validate_RejectsUnknownUploadSource(t *testing.T) {
	t.Parallel()
	p := validPluginSpec()
	p.UploadSource = "magic"
	if err := p.Validate(); !errors.Is(err, ErrInvalidPlugin) {
		t.Fatalf("err = %v, want ErrInvalidPlugin", err)
	}
}

func TestPluginSpec_Validate_ZipMustNotCarryGitHubFields(t *testing.T) {
	t.Parallel()
	p := validPluginSpec()
	p.UploadSource = UploadSourceZip
	p.GitHubRepo = "owner/repo"
	err := p.Validate()
	if !errors.Is(err, ErrInvalidPlugin) {
		t.Fatalf("err = %v, want ErrInvalidPlugin", err)
	}
}

func TestPluginSpec_Validate_GitHubRequiresRepo(t *testing.T) {
	t.Parallel()
	p := validPluginSpec()
	p.UploadSource = UploadSourceGitHub
	p.GitHubRepo = ""
	err := p.Validate()
	if !errors.Is(err, ErrInvalidPlugin) {
		t.Fatalf("err = %v, want ErrInvalidPlugin", err)
	}
}

func TestPluginSpec_Validate_GitHubHappyPath(t *testing.T) {
	t.Parallel()
	p := validPluginSpec()
	p.UploadSource = UploadSourceGitHub
	p.GitHubRepo = "owner/repo"
	p.GitHubRef = "main"
	p.GitHubPath = "plugins/foo"
	if err := p.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestSpec_PluginRoundtripJSON(t *testing.T) {
	t.Parallel()
	spec := Spec{
		SchemaVersion: SchemaVersionCurrent,
		Kind:          KindPlugin,
		Plugin:        ptrPluginSpec(validPluginSpec()),
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded Spec
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Kind != KindPlugin {
		t.Fatalf("decoded.Kind = %q, want %q", decoded.Kind, KindPlugin)
	}
	if decoded.MCP != nil || decoded.Skill != nil {
		t.Fatalf("decoded has unexpected mcp/skill body: %+v", decoded)
	}
	if decoded.Plugin == nil {
		t.Fatal("decoded.Plugin is nil")
	}
	if decoded.Plugin.Name != spec.Plugin.Name {
		t.Fatalf("name mismatch: %q vs %q", decoded.Plugin.Name, spec.Plugin.Name)
	}
	if decoded.Plugin.SHA256 != spec.Plugin.SHA256 {
		t.Fatalf("sha256 mismatch")
	}
	if err := decoded.Validate(); err != nil {
		t.Fatalf("decoded validate: %v", err)
	}
}

func TestSpec_KindPlugin_RejectsCrossBody(t *testing.T) {
	t.Parallel()
	spec := Spec{
		SchemaVersion: SchemaVersionCurrent,
		Kind:          KindPlugin,
		Plugin:        ptrPluginSpec(validPluginSpec()),
		MCP:           &MCPSpec{},
	}
	err := spec.Validate()
	if !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("err = %v, want ErrInvalidSpec", err)
	}
}

func TestSpec_KindPlugin_RejectsMissingBody(t *testing.T) {
	t.Parallel()
	spec := Spec{
		SchemaVersion: SchemaVersionCurrent,
		Kind:          KindPlugin,
	}
	err := spec.Validate()
	if !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("err = %v, want ErrInvalidSpec", err)
	}
}

func TestSpec_UnmarshalJSON_KindPluginEmptyBody(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"schema_version":1,"kind":"plugin"}`)
	var spec Spec
	err := json.Unmarshal(raw, &spec)
	if !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("err = %v, want ErrInvalidSpec", err)
	}
}

func ptrPluginSpec(p PluginSpec) *PluginSpec { return &p }
