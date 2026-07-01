package agentdaemon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// stubPluginPresigner is a deterministic PluginPresigner for tests. It
// records every key it was asked to presign and returns a canned URL
// formed by prefixing https://oss-fake/ to the key. Set ReturnErr to
// simulate signer failures.
type stubPluginPresigner struct {
	calls     []stubPluginPresignerCall
	ReturnErr error
}

type stubPluginPresignerCall struct {
	Key string
	TTL time.Duration
}

func (s *stubPluginPresigner) PresignGet(_ context.Context, key string, ttl time.Duration) (string, time.Time, error) {
	s.calls = append(s.calls, stubPluginPresignerCall{Key: key, TTL: ttl})
	if s.ReturnErr != nil {
		return "", time.Time{}, s.ReturnErr
	}
	return "https://oss-fake/" + key, time.Now().Add(ttl), nil
}

// newPluginRow constructs an EnabledCapabilityRead whose CanonicalSpec
// carries a fully-populated PluginSpec. Helper kept separate from the
// existing newSkillRow / newMCPRow because plugin tests almost always
// vary OssKey/SHA256 specifically and a generic helper would obscure
// that.
func newPluginRow(t *testing.T, id, name, ossKey, sha256 string) store.EnabledCapabilityRead {
	t.Helper()
	spec := canonical.Spec{
		SchemaVersion: canonical.SchemaVersionCurrent,
		Kind:          canonical.KindPlugin,
		Plugin: &canonical.PluginSpec{
			Name:         name,
			Version:      "1.0.0",
			Description:  "test plugin " + id,
			OssKey:       ossKey,
			SHA256:       sha256,
			UploadSource: canonical.UploadSourceZip,
		},
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal plugin spec: %v", err)
	}
	return store.EnabledCapabilityRead{
		CapabilityID:  id,
		Name:          name,
		Type:          "plugin",
		CanonicalSpec: raw,
		// Mirror the capability_version columns the squashed init
		// adds — the connector reads from these (not from the jsonb
		// copy) for the storage breadcrumbs.
		OssKey: ossKey,
		SHA256: sha256,
	}
}

// validSHA256 is a real 64-char hex digest the canonical.PluginSpec.Validate
// regex accepts. Content unrelated to anything — we never hash the test
// payload, the validator just checks shape.
const validSHA256 = "ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb"

func TestResolveCapabilityAdditions_PluginHappyPath(t *testing.T) {
	t.Parallel()
	presigner := &stubPluginPresigner{}
	c := &Connector{
		capabilities: stubCapabilityStore{
			rows: []store.EnabledCapabilityRead{
				newPluginRow(t, "p1", "my-plugin", "capabilities/plugins/u1/my-plugin.zip", validSHA256),
			},
		},
		oss: presigner,
		log: discardLogger(),
	}
	additions, err := c.resolveCapabilityAdditions(context.Background(), defaultPromptInput(), "claude_code")
	if err != nil {
		t.Fatalf("resolveCapabilityAdditions: %v", err)
	}
	if len(additions.Plugins) != 1 {
		t.Fatalf("plugin count = %d, want 1", len(additions.Plugins))
	}
	p := additions.Plugins[0]
	if p.Name != "my-plugin" {
		t.Fatalf("name = %q", p.Name)
	}
	if p.Version != "1.0.0" {
		t.Fatalf("version = %q", p.Version)
	}
	if p.SHA256 != validSHA256 {
		t.Fatalf("sha256 = %q", p.SHA256)
	}
	if p.DownloadURL != "https://oss-fake/capabilities/plugins/u1/my-plugin.zip" {
		t.Fatalf("download_url = %q", p.DownloadURL)
	}
	if len(presigner.calls) != 1 {
		t.Fatalf("presigner calls = %d, want 1", len(presigner.calls))
	}
	if presigner.calls[0].TTL != ossPresignTTL {
		t.Fatalf("ttl = %v, want %v", presigner.calls[0].TTL, ossPresignTTL)
	}
}

func TestResolveCapabilityAdditions_PluginSkipsWhenPresignerNil(t *testing.T) {
	t.Parallel()
	// PluginPresigner=nil (OSS not configured) → silently skip the
	// plugin row, no error. Operator sees a warning in the log.
	c := &Connector{
		capabilities: stubCapabilityStore{
			rows: []store.EnabledCapabilityRead{
				newPluginRow(t, "p1", "my-plugin", "k", validSHA256),
			},
		},
		log: discardLogger(),
	}
	additions, err := c.resolveCapabilityAdditions(context.Background(), defaultPromptInput(), "claude_code")
	if err != nil {
		t.Fatalf("resolveCapabilityAdditions: %v", err)
	}
	if len(additions.Plugins) != 0 {
		t.Fatalf("plugin count = %d, want 0 (nil presigner)", len(additions.Plugins))
	}
}

func TestResolveCapabilityAdditions_PluginPresignFailurePropagates(t *testing.T) {
	t.Parallel()
	// Signer failure must surface — silently dropping a plugin the
	// user enabled would be a confusing UX (Claude launches without
	// the plugin and nobody knows why).
	signerErr := errors.New("signer exploded")
	c := &Connector{
		capabilities: stubCapabilityStore{
			rows: []store.EnabledCapabilityRead{
				newPluginRow(t, "p1", "my-plugin", "k", validSHA256),
			},
		},
		oss: &stubPluginPresigner{ReturnErr: signerErr},
		log: discardLogger(),
	}
	_, err := c.resolveCapabilityAdditions(context.Background(), defaultPromptInput(), "claude_code")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// The connector deliberately does NOT wrap with %w (so presigned
	// URLs embedded in *url.Error don't survive the wrap chain). The
	// error message must still mention the inner cause.
	if !strings.Contains(err.Error(), "signer exploded") {
		t.Fatalf("err = %v, want it to mention 'signer exploded'", err)
	}
}

func TestResolveCapabilityAdditions_PluginKindMismatchErrors(t *testing.T) {
	t.Parallel()
	// type="plugin" but canonical_spec.kind="skill" → loud error.
	// Lying canonical_spec is a data-integrity bug worth surfacing.
	skillSpec := canonical.Spec{
		SchemaVersion: canonical.SchemaVersionCurrent,
		Kind:          canonical.KindSkill,
		Skill:         &canonical.SkillSpec{Slug: "x", Instruction: "y"},
	}
	raw, _ := json.Marshal(skillSpec)
	c := &Connector{
		capabilities: stubCapabilityStore{
			rows: []store.EnabledCapabilityRead{
				// OssKey column populated → connector reaches the
				// kind-mismatch check (without it, the row would
				// be silently skipped at the empty-column guard).
				{CapabilityID: "p1", Name: "n", Type: "plugin", CanonicalSpec: raw, OssKey: "capabilities/plugins/u/x.zip", SHA256: validSHA256},
			},
		},
		oss: &stubPluginPresigner{},
		log: discardLogger(),
	}
	_, err := c.resolveCapabilityAdditions(context.Background(), defaultPromptInput(), "claude_code")
	if err == nil {
		t.Fatal("expected error on kind mismatch")
	}
}

func TestResolveCapabilityAdditions_PluginEmptyCanonicalSpecSkipped(t *testing.T) {
	t.Parallel()
	// Legacy/malformed row with type="plugin" but empty canonical_spec
	// → silent skip. Logging would spam every prompt and the user
	// gets no actionable info from a row that should have been
	// repaired at import time.
	c := &Connector{
		capabilities: stubCapabilityStore{
			rows: []store.EnabledCapabilityRead{
				{CapabilityID: "p1", Name: "legacy", Type: "plugin"}, // no CanonicalSpec
			},
		},
		oss: &stubPluginPresigner{},
		log: discardLogger(),
	}
	additions, err := c.resolveCapabilityAdditions(context.Background(), defaultPromptInput(), "claude_code")
	if err != nil {
		t.Fatalf("resolveCapabilityAdditions: %v", err)
	}
	if len(additions.Plugins) != 0 {
		t.Fatalf("plugin count = %d, want 0", len(additions.Plugins))
	}
}

func TestResolveCapabilityAdditions_PluginsCoexistWithSkillAndMCP(t *testing.T) {
	t.Parallel()
	// Three capabilities in one agent: one of each type. All
	// three branches must populate their slot in capabilityAdditions
	// without interfering with the others.
	c := &Connector{
		capabilities: stubCapabilityStore{
			rows: []store.EnabledCapabilityRead{
				newSkillRow(t, "s1", "skill", "do the thing"),
				newMCPRow(t, "m1", "mcp", []canonical.MCPServer{{Name: "gh", Command: "x"}}, nil),
				newPluginRow(t, "p1", "plug", "capabilities/plugins/u1/plug.zip", validSHA256),
			},
		},
		oss: &stubPluginPresigner{},
		log: discardLogger(),
	}
	additions, err := c.resolveCapabilityAdditions(context.Background(), defaultPromptInput(), "claude_code")
	if err != nil {
		t.Fatalf("resolveCapabilityAdditions: %v", err)
	}
	if len(additions.Skills) != 1 {
		t.Fatalf("skill count = %d", len(additions.Skills))
	}
	if len(additions.MCPServers) != 1 {
		t.Fatalf("mcp count = %d", len(additions.MCPServers))
	}
	if len(additions.Plugins) != 1 {
		t.Fatalf("plugin count = %d", len(additions.Plugins))
	}
}

func TestResolveCapabilityAdditions_PluginDuplicateNameDedup(t *testing.T) {
	t.Parallel()
	// Two plugin capabilities vending plugin.json name="my-plugin" must
	// not both reach the daemon — installPlugins keys on Name and the
	// second would overwrite the first on disk. First wins.
	c := &Connector{
		capabilities: stubCapabilityStore{
			rows: []store.EnabledCapabilityRead{
				newPluginRow(t, "p1", "my-plugin", "capabilities/plugins/ws/uuid-a/my-plugin.zip", validSHA256),
				newPluginRow(t, "p2", "my-plugin", "capabilities/plugins/ws/uuid-b/my-plugin.zip", validSHA256),
			},
		},
		oss: &stubPluginPresigner{},
		log: discardLogger(),
	}
	additions, err := c.resolveCapabilityAdditions(context.Background(), defaultPromptInput(), "claude_code")
	if err != nil {
		t.Fatalf("resolveCapabilityAdditions: %v", err)
	}
	if len(additions.Plugins) != 1 {
		t.Fatalf("plugin count = %d, want 1 (first wins)", len(additions.Plugins))
	}
}

func TestMergePluginsIntoOptions_EmitsListOfMaps(t *testing.T) {
	t.Parallel()
	opts := map[string]any{}
	mergePluginsIntoOptions(opts, []ResolvedPlugin{
		{Name: "a", Version: "1", DownloadURL: "https://x/a.zip", SHA256: "aaa"},
		{Name: "b", Version: "2", DownloadURL: "https://x/b.zip", SHA256: "bbb"},
	})
	raw, ok := opts["plugins"].([]any)
	if !ok {
		t.Fatalf("opts[plugins] type = %T, want []any", opts["plugins"])
	}
	if len(raw) != 2 {
		t.Fatalf("len = %d", len(raw))
	}
	first := raw[0].(map[string]any)
	if first["name"] != "a" || first["download_url"] != "https://x/a.zip" {
		t.Fatalf("first entry = %v", first)
	}
}

func TestMergePluginsIntoOptions_EmptySliceIsNoop(t *testing.T) {
	t.Parallel()
	opts := map[string]any{}
	mergePluginsIntoOptions(opts, nil)
	if _, ok := opts["plugins"]; ok {
		t.Fatalf("opts[plugins] should be absent for empty input")
	}
}

func TestMergePluginsIntoOptions_OverrideWins(t *testing.T) {
	t.Parallel()
	// If opts already carries a "plugins" key (hand-configured
	// override), capability-resolved plugins MUST be dropped to
	// preserve the override as the authoritative source.
	preset := []any{map[string]any{"name": "override"}}
	opts := map[string]any{"plugins": preset}
	mergePluginsIntoOptions(opts, []ResolvedPlugin{
		{Name: "should-not-appear"},
	})
	got, ok := opts["plugins"].([]any)
	if !ok || len(got) != 1 {
		t.Fatalf("override should be preserved; got %v", opts["plugins"])
	}
	first := got[0].(map[string]any)
	if first["name"] != "override" {
		t.Fatalf("override entry mutated: %v", first)
	}
}
