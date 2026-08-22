package deepseekharness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func testProfileSpec(home string, port int) serverProfileSpec {
	return serverProfileSpec{
		Home: home,
		Port: port,
		Provider: providerConfig{
			Name:      "Parsar Gateway",
			BaseURL:   "https://example.invalid/v1",
			API:       "openai-completions",
			APIKeyEnv: "PARSAR_DSH_API_KEY",
			Model:     "deepseek/deepseek-v4-flash",
		},
		HasProvider: true,
	}
}

func TestWriteServerProfileLaysOutTheProfileTree(t *testing.T) {
	home := t.TempDir()
	if err := writeServerProfile(testProfileSpec(home, 45678)); err != nil {
		t.Fatalf("writeServerProfile: %v", err)
	}

	dir := filepath.Join(home, "profiles", serverProfileName)
	for _, name := range []string{"package.json", "cordis.yml", "cordis.patch.yml"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %v, want 0600", name, info.Mode().Perm())
		}
	}

	manifest, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if !strings.Contains(string(manifest), baseBundle) {
		t.Errorf("manifest does not declare the base bundle: %s", manifest)
	}
}

// rowsFromPatch decodes the generated patch layer into a name→config map
// for the insert rows and a set of overridden row ids.
func rowsFromPatch(t *testing.T, body []byte) (map[string]map[string]any, map[string]bool) {
	t.Helper()
	var entries []map[string]any
	if err := yaml.Unmarshal(body, &entries); err != nil {
		t.Fatalf("unmarshal patch: %v\n%s", err, body)
	}
	inserts := map[string]map[string]any{}
	overrides := map[string]bool{}
	for _, entry := range entries {
		if raw, ok := entry["insert"]; ok {
			list, ok := raw.([]any)
			if !ok {
				t.Fatalf("insert is %T, want a list", raw)
			}
			for _, item := range list {
				row, ok := item.(map[string]any)
				if !ok {
					t.Fatalf("insert item is %T", item)
				}
				name, _ := row["name"].(string)
				cfg, _ := row["config"].(map[string]any)
				inserts[name] = cfg
			}
			continue
		}
		if id, ok := entry["id"].(string); ok {
			overrides[id] = true
		}
	}
	return inserts, overrides
}

func TestServerPatchCarriesEveryRowTheGatewayNeeds(t *testing.T) {
	home := t.TempDir()
	body, err := renderServerPatch(testProfileSpec(home, 45678))
	if err != nil {
		t.Fatalf("renderServerPatch: %v", err)
	}
	inserts, overrides := rowsFromPatch(t, body)

	// Each of these was required to boot: dropping any one of them either
	// fails config validation or leaves /api answering 404.
	required := []string{
		"@deepseek-ai/dsh-storage",
		"@deepseek-ai/dsh-storage-json",
		"@deepseek-ai/dsh-storage-domain",
		"@deepseek-ai/dsh-workspace",
		"@deepseek-ai/dsh-host-directory-picker-auto",
		"@deepseek-ai/dsh-host-apiproxy",
		"@deepseek-ai/dsh-host-webserver",
		"@deepseek-ai/dsh-client-connection",
	}
	for _, name := range required {
		if _, ok := inserts[name]; !ok {
			t.Errorf("generated profile is missing plugin row %s", name)
		}
	}
	for _, id := range []string{"permission", "llm-pi-ai", "agent-default-model"} {
		if !overrides[id] {
			t.Errorf("generated profile does not override row %q", id)
		}
	}
}

func TestServerPatchPinsLoopbackAndTheAssignedPort(t *testing.T) {
	home := t.TempDir()
	body, err := renderServerPatch(testProfileSpec(home, 45678))
	if err != nil {
		t.Fatalf("renderServerPatch: %v", err)
	}
	inserts, _ := rowsFromPatch(t, body)

	web := inserts["@deepseek-ai/dsh-host-webserver"]
	if got := web["host"]; got != "127.0.0.1" {
		t.Errorf("web server host = %v, want 127.0.0.1", got)
	}
	if got := web["port"]; got != 45678 {
		t.Errorf("web server port = %v, want the assigned 45678", got)
	}

	// An empty trusted-host list is what keeps the unauthenticated gateway
	// reachable only from inside the sandbox.
	carrier := inserts["@deepseek-ai/dsh-client-connection"]
	hosts, ok := carrier["trustedHosts"]
	if !ok {
		t.Fatal("carrier row does not set trustedHosts")
	}
	if list, _ := hosts.([]any); len(list) != 0 {
		t.Errorf("trustedHosts = %v, want empty", hosts)
	}

	gateway := inserts["@deepseek-ai/dsh-host-apiproxy"]
	if got := gateway["nativeOpen"]; got != false {
		t.Errorf("nativeOpen = %v, want false", got)
	}
}

func TestServerPatchIncludesMCPClientWithoutLoggingSecrets(t *testing.T) {
	spec := testProfileSpec(t.TempDir(), 45678)
	rows, err := normaliseMCPRows(map[string]any{
		"proof": map[string]any{
			"url":     "https://mcp.example/mcp",
			"headers": map[string]any{"Authorization": "Bearer secret-marker"},
		},
	}, "/workspace")
	if err != nil {
		t.Fatal(err)
	}
	spec.MCPRows = rows
	body, err := renderServerPatch(spec)
	if err != nil {
		t.Fatalf("renderServerPatch: %v", err)
	}
	inserts, _ := rowsFromPatch(t, body)
	cfg := inserts["@deepseek-ai/dsh-mcp-client"]
	if cfg["transport"] != "streamable-http" || cfg["serverName"] != "proof" {
		t.Fatalf("mcp config = %v", cfg)
	}
	launch := serverLaunch{StateKey: "conversation", MCPRows: rows}
	if got := launch.key(); strings.Contains(got, "secret-marker") {
		t.Fatalf("server key leaked MCP header: %q", got)
	}
}

func TestServerPatchRootsStorageUnderTheHome(t *testing.T) {
	home := t.TempDir()
	body, err := renderServerPatch(testProfileSpec(home, 1234))
	if err != nil {
		t.Fatalf("renderServerPatch: %v", err)
	}
	inserts, _ := rowsFromPatch(t, body)

	root, _ := inserts["@deepseek-ai/dsh-storage-json"]["root"].(string)
	if root != filepath.Join(home, "storages") {
		t.Errorf("storage root = %q, want %q", root, filepath.Join(home, "storages"))
	}
	// dsh's own profiles express this with a `!!js` expression. The
	// generated file must stay a plain literal so nothing here depends on
	// dsh evaluating script in a config layer.
	if strings.Contains(string(body), "!!js") {
		t.Errorf("generated profile smuggled a script tag:\n%s", body)
	}

	backend, _ := inserts["@deepseek-ai/dsh-storage-domain"]["backend"].(string)
	if backend != "json" {
		t.Errorf("storage-domain backend = %q, want json", backend)
	}
}

func TestServerPatchPinsUnattendedPermissions(t *testing.T) {
	home := t.TempDir()
	body, err := renderServerPatch(testProfileSpec(home, 1234))
	if err != nil {
		t.Fatalf("renderServerPatch: %v", err)
	}
	var entries []map[string]any
	if err := yaml.Unmarshal(body, &entries); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var perm map[string]any
	for _, entry := range entries {
		if entry["id"] == "permission" {
			perm, _ = entry["config"].(map[string]any)
		}
	}
	if perm == nil {
		t.Fatal("no permission row")
	}
	if perm["defaultPreset"] != unattendedPreset {
		t.Errorf("defaultPreset = %v, want %s", perm["defaultPreset"], unattendedPreset)
	}
	presets, _ := perm["presets"].(map[string]any)
	preset, _ := presets[unattendedPreset].(map[string]any)
	if preset["sandbox"] != sandboxPermissionMode || preset["approval"] != "never" {
		t.Errorf("unattended preset = %v, want workspace-write / never", preset)
	}
}

// TestMaterialiseServerProfileForManualBoot writes a profile to a real
// DSH_HOME so a live dsh boot can be driven against generated (not
// hand-written) config. Skipped unless the path is supplied, because it
// needs a dsh install.
func TestMaterialiseServerProfileForManualBoot(t *testing.T) {
	home := strings.TrimSpace(os.Getenv("PARSAR_DSH_PROFILE_OUT"))
	if home == "" {
		t.Skip("set PARSAR_DSH_PROFILE_OUT to materialise a bootable profile")
	}
	port := 3400
	spec := testProfileSpec(home, port)
	spec.Provider.BaseURL = "https://api.sandbase.ai/v1"
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("mkdir home: %v", err)
	}
	if err := writeServerProfile(spec); err != nil {
		t.Fatalf("writeServerProfile: %v", err)
	}
	t.Logf("wrote profile %s into %s (port %d)", serverProfileName, home, port)
}
