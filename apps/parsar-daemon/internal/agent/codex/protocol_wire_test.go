package codex

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestThreadStartParams_SandboxIsKebabString pins the v0.141+ wire
// format reviewer caught the hard way: codex's ThreadStartParams.sandbox
// is a kebab-case string ("read-only" / "workspace-write" /
// "danger-full-access"), NOT the legacy {"type":"dangerFullAccess"}
// tagged object on a sandboxPolicy field.
//
// Sending the legacy shape used to terminate every turn in <1s with an
// empty agent message body, because codex silently fell back to
// read-only when it didn't recognise the field name.
func TestThreadStartParams_SandboxIsKebabString(t *testing.T) {
	cases := []struct {
		name string
		mode SandboxMode
		want string
	}{
		{"read-only", SandboxReadOnly, "read-only"},
		{"workspace-write", SandboxWorkspaceWrite, "workspace-write"},
		{"danger-full-access", SandboxDangerFullAcces, "danger-full-access"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params := ThreadStartParams{
				Cwd:            "/workspace",
				Model:          "gpt-5.5",
				ApprovalPolicy: SilentGranularPolicy(),
				Sandbox:        tc.mode,
			}
			raw, err := json.Marshal(params)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			body := string(raw)
			if !strings.Contains(body, `"sandbox":"`+tc.want+`"`) {
				t.Fatalf("wire body missing sandbox=%q: %s", tc.want, body)
			}
			// The legacy field name MUST be gone. Even shipping it as
			// "sandboxPolicy" alongside the new field would confuse
			// future-version codex servers that strict-deserialise.
			if strings.Contains(body, `"sandboxPolicy"`) {
				t.Fatalf("legacy sandboxPolicy field leaked into wire: %s", body)
			}
			// And the kebab-case value must NOT collide with the old
			// camelCase enum names.
			for _, oldName := range []string{`"readOnly"`, `"workspaceWrite"`, `"dangerFullAccess"`} {
				if strings.Contains(body, oldName) {
					t.Fatalf("legacy camelCase enum value %s leaked: %s", oldName, body)
				}
			}
		})
	}
}

// TestThreadStartParams_OmitsEmptyOptionalFields pins that model_provider
// and other optional fields don't leak null/empty values into the wire.
// codex would reject malformed enum values otherwise.
func TestThreadStartParams_OmitsEmptyOptionalFields(t *testing.T) {
	params := ThreadStartParams{
		Cwd:            "/workspace",
		ApprovalPolicy: SilentGranularPolicy(),
		Sandbox:        SandboxDangerFullAcces,
		// Model, ModelProvider, DeveloperInstructions deliberately empty
	}
	raw, _ := json.Marshal(params)
	body := string(raw)
	for _, leak := range []string{
		`"model":""`,
		`"modelProvider":""`,
		`"developerInstructions":""`,
	} {
		if strings.Contains(body, leak) {
			t.Errorf("empty optional field leaked: %s in %s", leak, body)
		}
	}
}

// TestThreadStartParams_ModelProviderIsCamelCaseField confirms the
// model_provider override (used by injectCodexManagedModel to pin codex
// to the [model_providers.parsar] config block) actually reaches
// codex via the v2 thread/start params, not just via the -c CLI
// override. Sending it on both paths is belt + suspenders.
func TestThreadStartParams_ModelProviderIsCamelCaseField(t *testing.T) {
	params := ThreadStartParams{
		Cwd:            "/workspace",
		Model:          "gpt-5.5",
		ModelProvider:  "parsar",
		ApprovalPolicy: SilentGranularPolicy(),
		Sandbox:        SandboxDangerFullAcces,
	}
	raw, _ := json.Marshal(params)
	body := string(raw)
	if !strings.Contains(body, `"modelProvider":"parsar"`) {
		t.Fatalf("modelProvider missing or wrong case in wire: %s", body)
	}
	// snake_case would silently be ignored by codex's serde rename_all
	// = camelCase, so guard against accidental drift.
	if strings.Contains(body, `"model_provider"`) {
		t.Fatalf("snake_case model_provider leaked: %s", body)
	}
}

func TestTurnStartParams_CollaborationModeUsesPlanWireShape(t *testing.T) {
	developerInstructions := "stay within the configured workspace"
	params := TurnStartParams{
		ThreadID: "thread-1",
		Input:    FirstUserInput("ask me a question"),
		CollaborationMode: &CollaborationMode{
			Mode: CollaborationModePlan,
			Settings: CollaborationModeSettings{
				Model:                 "MiniMax-M3",
				DeveloperInstructions: &developerInstructions,
			},
		},
	}
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(raw)
	if !strings.Contains(body, `"collaborationMode":{"mode":"plan","settings":{"model":"MiniMax-M3","developer_instructions":"stay within the configured workspace"}}`) {
		t.Fatalf("collaboration mode missing or malformed: %s", body)
	}
	if strings.Contains(body, `"collaboration_mode"`) {
		t.Fatalf("snake_case collaboration_mode leaked: %s", body)
	}
}
