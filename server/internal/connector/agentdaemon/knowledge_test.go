package agentdaemon

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

func knowledgeSpecJSON(t *testing.T, content string) []byte {
	t.Helper()
	raw, err := json.Marshal(canonical.Spec{SchemaVersion: 1, Kind: canonical.KindKnowledge, Knowledge: &canonical.KnowledgeSpec{Documents: []canonical.KnowledgeDocument{{Name: "policy.md", Content: content}}}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestKnowledgeContextAcrossEnginesAndVersions(t *testing.T) {
	for _, engine := range []string{"claude_code", "codex", "opencode", "pi"} {
		for _, mode := range []string{"pinned", "latest"} {
			t.Run(engine+"/"+mode, func(t *testing.T) {
				row := store.EnabledCapabilityRead{CapabilityID: "knowledge", Name: "Onboarding", Type: "knowledge", PinningMode: mode, Version: "1.0.0", CanonicalSpec: knowledgeSpecJSON(t, "OLD-FACT"), LatestVersion: "1.0.1", LatestCanonicalSpec: knowledgeSpecJSON(t, "NEW-FACT")}
				c := &Connector{capabilities: stubCapabilityStore{rows: []store.EnabledCapabilityRead{row}}, log: discardLogger()}
				got, err := c.resolveCapabilityAdditions(t.Context(), defaultPromptInput(), engine)
				if err != nil {
					t.Fatal(err)
				}
				for _, key := range []string{"system_prompt", "override_system_prompt"} {
					opts := map[string]any{key: "Agent instructions"}
					if err := appendKnowledgeContext(opts, got.Knowledge); err != nil {
						t.Fatal(err)
					}
					value := opts[key].(string)
					want, absent := "OLD-FACT", "NEW-FACT"
					if mode == "latest" {
						want, absent = absent, want
					}
					if !strings.HasPrefix(value, "Agent instructions\n") || !strings.Contains(value, want) || strings.Contains(value, absent) {
						t.Fatalf("wrong context: %s", value)
					}
				}
			})
		}
	}
}

func TestKnowledgeAbsentAndCombinedLimit(t *testing.T) {
	opts := map[string]any{"system_prompt": "unchanged"}
	if err := appendKnowledgeContext(opts, nil); err != nil || opts["system_prompt"] != "unchanged" {
		t.Fatal("unbound Agent changed")
	}
	huge := []resolvedKnowledge{{Documents: []canonical.KnowledgeDocument{{Name: "large", Content: strings.Repeat("x", 65*1024)}}}}
	if err := appendKnowledgeContext(opts, huge); err == nil || opts["system_prompt"] != "unchanged" {
		t.Fatal("oversized context was sent or truncated")
	}
}

func TestBuildAgentOptionsInjectsKnowledge(t *testing.T) {
	c := &Connector{log: discardLogger(), capabilities: stubCapabilityStore{rows: []store.EnabledCapabilityRead{{CapabilityID: "kb", Type: "knowledge", CanonicalSpec: knowledgeSpecJSON(t, "PARSAR-KNOWLEDGE-OK")}}}}
	opts, err := c.buildAgentOptions(t.Context(), defaultPromptInput())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stringFromMap(opts, "system_prompt"), "PARSAR-KNOWLEDGE-OK") {
		t.Fatal("knowledge absent from dispatched options")
	}
}
