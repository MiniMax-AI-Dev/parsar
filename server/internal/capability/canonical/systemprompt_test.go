package canonical

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestSystemPromptSpec_RoundTrip(t *testing.T) {
	cases := []struct {
		name string
		in   Spec
	}{
		{
			name: "append default mode",
			in: Spec{
				SchemaVersion: SchemaVersionCurrent,
				Kind:          KindSystemPrompt,
				SystemPrompt:  &SystemPromptSpec{Prompt: "Always answer in Chinese."},
			},
		},
		{
			name: "override mode",
			in: Spec{
				SchemaVersion: SchemaVersionCurrent,
				Kind:          KindSystemPrompt,
				SystemPrompt:  &SystemPromptSpec{Prompt: "You are pirate.", Mode: SystemPromptModeOverride},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := json.Marshal(tc.in)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var got Spec
			if err := json.Unmarshal(encoded, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if err := got.Validate(); err != nil {
				t.Fatalf("validate: %v", err)
			}
			if got.SystemPrompt == nil {
				t.Fatalf("system_prompt body lost in round-trip")
			}
			if got.SystemPrompt.Prompt != tc.in.SystemPrompt.Prompt {
				t.Fatalf("prompt mismatch: want %q got %q", tc.in.SystemPrompt.Prompt, got.SystemPrompt.Prompt)
			}
		})
	}
}

func TestSystemPromptSpec_ValidateRejectsEmptyPrompt(t *testing.T) {
	s := Spec{SchemaVersion: 1, Kind: KindSystemPrompt, SystemPrompt: &SystemPromptSpec{Prompt: "   "}}
	if err := s.Validate(); err == nil || !errors.Is(err, ErrInvalidSystemPrompt) {
		t.Fatalf("expected ErrInvalidSystemPrompt, got %v", err)
	}
}

func TestSystemPromptSpec_ValidateRejectsUnknownMode(t *testing.T) {
	s := Spec{SchemaVersion: 1, Kind: KindSystemPrompt, SystemPrompt: &SystemPromptSpec{Prompt: "hi", Mode: "bogus"}}
	if err := s.Validate(); err == nil || !errors.Is(err, ErrInvalidSystemPrompt) {
		t.Fatalf("expected ErrInvalidSystemPrompt, got %v", err)
	}
}

func TestSystemPromptSpec_UnmarshalRejectsMissingBody(t *testing.T) {
	var got Spec
	err := json.Unmarshal([]byte(`{"schema_version":1,"kind":"system_prompt"}`), &got)
	if err == nil || !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("expected ErrInvalidSpec for missing system_prompt body, got %v", err)
	}
}

func TestSystemPromptSpec_ResolvedModeDefaultsToAppend(t *testing.T) {
	s := SystemPromptSpec{Prompt: "x"}
	if got := s.ResolvedMode(); got != SystemPromptModeAppend {
		t.Fatalf("default mode = %q, want append", got)
	}
}
