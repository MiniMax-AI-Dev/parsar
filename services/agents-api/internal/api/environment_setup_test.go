package api

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestEnvironmentSetupSharedParsingAndConfidentialSnapshot(t *testing.T) {
	raw := []byte(`{"env":{"TOKEN":"private-env-canary","QUOTED":"'\n$(false)"},"packages":{"npm":["is-number@7.0.0"],"python":["packaging==26.0"]},"setup_commands":[{"command":"printf private-command-canary > result","cwd":null},{"command":"pwd","cwd":"/workspace/sub"}]}`)
	input, err := decodeTemplateInput(raw)
	if err != nil || !input.SetEnv || !input.SetSetup || !input.SetPackages || len(input.Initialization.Commands) != 2 {
		t.Fatal("template setup input", err)
	}
	var request decodedSessionRequest
	if err := json.Unmarshal(append(append([]byte(`{"agent":{"model":"test"},"environment":{"type":"openai_hosted",`), raw[1:len(raw)-1]...), []byte(`}}`)...), &request); err != nil {
		t.Fatal(err)
	}
	decoded, err := request.validated()
	if err != nil {
		t.Fatal(err)
	}
	configuration, err := resolve(decoded, "tenant", "key", nil)
	if err != nil || bytes.Contains(configuration, []byte("canary")) || !bytes.Contains(configuration, []byte("is-number@7.0.0")) {
		t.Fatal("confidential input in ordinary configuration", err)
	}
	if decoded.initialization.Env["TOKEN"] != input.Initialization.Env["TOKEN"] || len(decoded.initialization.Commands) != 2 {
		t.Fatal("inline and template parsing diverged")
	}
	for _, invalid := range []string{`{"env":{"OPENAI_API_KEY":"x"}}`, `{"env":{"CODEX_HOME":"x"}}`, `{"env":{"PARSAR_HOME":"x"}}`, `{"env":{"BAD-NAME":"x"}}`, `{"env":{"VALUE":null}}`, `{"setup_commands":[null]}`, `{"setup_commands":[{}]}`, `{"setup_commands":[{"command":null}]}`, `{"setup_commands":[{"command":"pwd","cwd":""}]}`, `{"packages":{"python":[null]}}`, `{"packages":{"npm":["--ignore-scripts"]}}`} {
		if _, err := decodeTemplateInput([]byte(invalid)); err == nil {
			t.Fatal("invalid setup accepted", invalid)
		}
	}
	cleared, err := decodeTemplateInput([]byte(`{"env":null,"setup_commands":null,"packages":null}`))
	if err != nil || !cleared.Initialization.Empty() || !cleared.SetEnv || !cleared.SetSetup || !cleared.SetPackages {
		t.Fatal("nullable replacements", err)
	}
}

func TestSavedAgentCreationIntentRetainsInlineSetup(t *testing.T) {
	intent := func(environment string) json.RawMessage {
		t.Helper()
		var request decodedSessionRequest
		if err := json.Unmarshal([]byte(`{"agent_id":"saved","environment":`+environment+`}`), &request); err != nil {
			t.Fatal(err)
		}
		input, err := request.validated()
		if err != nil {
			t.Fatal(err)
		}
		value, err := sessionCreationRequest(input, nil)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	variants := []string{
		`{"type":"openai_hosted"}`,
		`{"type":"openai_hosted","env":{"TOKEN":"first"}}`,
		`{"type":"openai_hosted","env":{"TOKEN":"changed"}}`,
		`{"type":"openai_hosted","setup_commands":[{"command":"first"}]}`,
		`{"type":"openai_hosted","setup_commands":[{"command":"changed"}]}`,
		`{"type":"openai_hosted","env":{"TOKEN":"first"},"setup_commands":[{"command":"first"}]}`,
	}
	for i, first := range variants {
		identity := intent(first)
		if !bytes.Equal(identity, intent(first)) {
			t.Fatal("unchanged intent differs")
		}
		for j, second := range variants {
			if i != j && bytes.Equal(identity, intent(second)) {
				t.Fatalf("changed, added or removed setup lost from intent: %d, %d", i, j)
			}
		}
	}
}
