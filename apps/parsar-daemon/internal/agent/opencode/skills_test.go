package opencode

import (
	"encoding/json"
	"testing"
)

func TestWithOpenCodeSkillRootPreservesExistingConfigDir(t *testing.T) {
	t.Setenv(openCodeConfigDirEnv, "/user/opencode")
	t.Setenv(openCodeConfigContentEnv, `{"agent":{"review":{}},"skills":{"paths":["/user/skills"],"urls":["https://example.com/skills"]}}`)

	opts, err := withOpenCodeSkillRoot(map[string]any{
		"env": map[string]any{"KEEP": "value"},
	}, "/managed/skills")
	if err != nil {
		t.Fatalf("withOpenCodeSkillRoot: %v", err)
	}
	env := opts["env"].(map[string]any)
	if _, overridden := env[openCodeConfigDirEnv]; overridden {
		t.Fatalf("%s must remain inherited", openCodeConfigDirEnv)
	}
	if env["KEEP"] != "value" {
		t.Fatalf("existing env was not preserved: %v", env)
	}
	var config struct {
		Agent  map[string]any `json:"agent"`
		Skills struct {
			Paths []string `json:"paths"`
			URLs  []string `json:"urls"`
		} `json:"skills"`
	}
	if err := json.Unmarshal([]byte(env[openCodeConfigContentEnv].(string)), &config); err != nil {
		t.Fatal(err)
	}
	if len(config.Agent) != 1 || len(config.Skills.Paths) != 2 || config.Skills.Paths[1] != "/managed/skills" || len(config.Skills.URLs) != 1 {
		t.Fatalf("merged config = %+v", config)
	}
}
