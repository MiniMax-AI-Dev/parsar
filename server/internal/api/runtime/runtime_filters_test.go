package runtime

import (
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

func TestRuntimeListFilterHelpersClassifyPlacement(t *testing.T) {
	local := store.RuntimeRead{
		Type:     store.RuntimeTypeAgentDaemon,
		Provider: store.RuntimeProviderAgentDaemon,
		Config:   map[string]any{},
	}
	if got := runtimePlacement(local); got != "local_device" {
		t.Fatalf("local daemon placement = %q, want local_device", got)
	}

	sandboxByProvider := store.RuntimeRead{
		Type:     store.RuntimeTypeAgentDaemon,
		Provider: store.RuntimeProviderAgentDaemonSandbox,
		Config:   map[string]any{},
	}
	if got := runtimePlacement(sandboxByProvider); got != "cloud_sandbox" {
		t.Fatalf("sandbox daemon provider placement = %q, want cloud_sandbox", got)
	}

	sandboxByConfig := store.RuntimeRead{
		Type:     store.RuntimeTypeAgentDaemon,
		Provider: store.RuntimeProviderAgentDaemon,
		Config: map[string]any{
			"created_by": "sandbox_provider",
		},
	}
	if got := runtimePlacement(sandboxByConfig); got != "cloud_sandbox" {
		t.Fatalf("sandbox daemon config placement = %q, want cloud_sandbox", got)
	}

	external := store.RuntimeRead{
		Type:     store.RuntimeTypeExternal,
		Provider: store.RuntimeProviderHTTPAgent,
		Config:   map[string]any{},
	}
	if got := runtimePlacement(external); got != "external_agent" {
		t.Fatalf("external placement = %q, want external_agent", got)
	}
}

func TestRuntimeListFilterHelpersAgentKindSupport(t *testing.T) {
	legacyDaemon := store.RuntimeRead{
		Type:     store.RuntimeTypeAgentDaemon,
		Provider: store.RuntimeProviderAgentDaemon,
		Config:   map[string]any{},
	}
	if !runtimeSupportsAgentKind(legacyDaemon, "claude_code") {
		t.Fatal("legacy daemon without capability snapshot should match claude_code")
	}
	if runtimeSupportsAgentKind(legacyDaemon, "opencode") {
		t.Fatal("legacy daemon without capability snapshot should not match opencode")
	}

	advertisedNames := store.RuntimeRead{
		Type:     store.RuntimeTypeAgentDaemon,
		Provider: store.RuntimeProviderAgentDaemon,
		Config: map[string]any{
			"supported_agent_kind_names": []any{"claude_code", "opencode"},
		},
	}
	if !runtimeSupportsAgentKind(advertisedNames, "opencode") {
		t.Fatal("supported_agent_kind_names should match opencode")
	}

	advertisedKinds := store.RuntimeRead{
		Type:     store.RuntimeTypeAgentDaemon,
		Provider: store.RuntimeProviderAgentDaemon,
		Config: map[string]any{
			"supported_agent_kinds": []any{
				map[string]any{"kind": "claude_code", "available": true},
				map[string]any{"kind": "opencode", "available": false},
			},
		},
	}
	if !runtimeSupportsAgentKind(advertisedKinds, "claude_code") {
		t.Fatal("available supported_agent_kinds entry should match claude_code")
	}
	if runtimeSupportsAgentKind(advertisedKinds, "opencode") {
		t.Fatal("unavailable supported_agent_kinds entry should not match opencode")
	}
}

func TestRuntimeMatchesListFilters(t *testing.T) {
	rt := store.RuntimeRead{
		Type:     store.RuntimeTypeAgentDaemon,
		Provider: store.RuntimeProviderAgentDaemon,
		Liveness: store.RuntimeLivenessOnline,
		Config: map[string]any{
			"supported_agent_kind_names": []any{"claude_code"},
		},
	}

	if !runtimeMatchesListFilters(rt, runtimeListFilters{Placement: "local_device", Liveness: store.RuntimeLivenessOnline, AgentKind: "claude_code"}) {
		t.Fatal("runtime should match local online claude_code filters")
	}
	if runtimeMatchesListFilters(rt, runtimeListFilters{Placement: "cloud_sandbox"}) {
		t.Fatal("local runtime should not match cloud_sandbox placement")
	}
	if runtimeMatchesListFilters(rt, runtimeListFilters{Liveness: store.RuntimeLivenessOffline}) {
		t.Fatal("online runtime should not match offline liveness")
	}
	if runtimeMatchesListFilters(rt, runtimeListFilters{AgentKind: "opencode"}) {
		t.Fatal("claude-only runtime should not match opencode")
	}
}
