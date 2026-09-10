package agentdaemon

import (
	"strings"
	"testing"
)

func TestSkillUploadContextIsRunScopedAndSecretFreePrompt(t *testing.T) {
	for _, key := range []string{"system_prompt", "override_system_prompt"} {
		t.Run(key, func(t *testing.T) {
			c := &Connector{skillUploadToken: func(runID string) (string, error) { return "secret-" + runID, nil }}
			env := map[string]any{"MODEL_KEY": "preserved"}
			for _, runID := range []string{"run-a", "run-b"} {
				opts := map[string]any{key: "existing instructions", "env": env}
				if err := c.applySkillUpload(opts, runID); err != nil {
					t.Fatal(err)
				}
				got := opts["env"].(map[string]any)
				if got["PARSAR_CAPABILITY_UPLOAD_TOKEN"] != "secret-"+runID || got["MODEL_KEY"] != "preserved" {
					t.Fatal("wrong request environment")
				}
				prompt := opts[key].(string)
				if !strings.HasPrefix(prompt, "existing instructions") || !strings.Contains(prompt, "parsar plugin add") || strings.Contains(prompt, "secret-") {
					t.Fatal("prompt lost instructions or exposed token")
				}
			}
			if len(env) != 1 {
				t.Fatal("mutated input environment")
			}
		})
	}
}
