package canonical

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestBundleSpec_Validate_Valid(t *testing.T) {
	t.Parallel()
	specs := []BundleSpec{
		{Name: "@internal/hotel-ops", Version: "1.0.0", Skills: []BundleSkill{{Slug: "greeting", Instruction: "Hello!"}}},
		{Name: "my-plugin", Version: "0.1.0", ServerEntry: "./server/index.js"},
		{Name: "ui-only", Version: "2.0.0", ClientEntry: "./client/index.js"},
		{Name: "full", Version: "1.0.0", ServerEntry: "./server/index.js", ClientEntry: "./client/index.js", Skills: []BundleSkill{{Slug: "s1", Instruction: "do x"}}},
	}
	for _, spec := range specs {
		if err := spec.Validate(); err != nil {
			t.Errorf("Validate(%q) = %v, want nil", spec.Name, err)
		}
	}
}

func TestBundleSpec_Validate_MissingName(t *testing.T) {
	t.Parallel()
	spec := BundleSpec{Version: "1.0.0", Skills: []BundleSkill{{Slug: "s", Instruction: "x"}}}
	err := spec.Validate()
	if err == nil || !errors.Is(err, ErrInvalidBundle) {
		t.Fatalf("expected ErrInvalidBundle for missing name, got %v", err)
	}
}

func TestBundleSpec_Validate_MissingVersion(t *testing.T) {
	t.Parallel()
	spec := BundleSpec{Name: "test", Skills: []BundleSkill{{Slug: "s", Instruction: "x"}}}
	err := spec.Validate()
	if err == nil || !errors.Is(err, ErrInvalidBundle) {
		t.Fatalf("expected ErrInvalidBundle for missing version, got %v", err)
	}
}

func TestBundleSpec_Validate_NoComponents(t *testing.T) {
	t.Parallel()
	spec := BundleSpec{Name: "empty", Version: "1.0.0"}
	err := spec.Validate()
	if err == nil || !errors.Is(err, ErrInvalidBundle) {
		t.Fatalf("expected ErrInvalidBundle for no components, got %v", err)
	}
}

func TestBundleSpec_Validate_SkillMissingSlug(t *testing.T) {
	t.Parallel()
	spec := BundleSpec{Name: "test", Version: "1.0.0", Skills: []BundleSkill{{Slug: "", Instruction: "x"}}}
	err := spec.Validate()
	if err == nil || !errors.Is(err, ErrInvalidBundle) {
		t.Fatalf("expected ErrInvalidBundle for missing slug, got %v", err)
	}
}

func TestBundleSpec_Validate_SkillMissingInstruction(t *testing.T) {
	t.Parallel()
	spec := BundleSpec{Name: "test", Version: "1.0.0", Skills: []BundleSkill{{Slug: "s", Instruction: ""}}}
	err := spec.Validate()
	if err == nil || !errors.Is(err, ErrInvalidBundle) {
		t.Fatalf("expected ErrInvalidBundle for missing instruction, got %v", err)
	}
}

func TestSpec_KindBundle_RoundTrip(t *testing.T) {
	t.Parallel()
	spec := Spec{
		SchemaVersion: SchemaVersionCurrent,
		Kind:          KindBundle,
		Bundle: &BundleSpec{
			Name:    "@internal/jira",
			Version: "1.2.0",
			Skills:  []BundleSkill{{Slug: "jira-workflow", Instruction: "Create issues..."}},
			Tools:   []string{"jira_create_issue"},
		},
	}
	if err := spec.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded Spec
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.Kind != KindBundle {
		t.Fatalf("decoded.Kind = %q, want %q", decoded.Kind, KindBundle)
	}
	if decoded.Bundle == nil {
		t.Fatal("decoded.Bundle is nil")
	}
	if decoded.Bundle.Name != "@internal/jira" {
		t.Fatalf("decoded.Bundle.Name = %q", decoded.Bundle.Name)
	}
	if len(decoded.Bundle.Skills) != 1 || decoded.Bundle.Skills[0].Slug != "jira-workflow" {
		t.Fatalf("decoded.Bundle.Skills = %+v", decoded.Bundle.Skills)
	}
}

func TestSpec_KindBundle_RejectsCrossBody(t *testing.T) {
	t.Parallel()
	spec := Spec{
		SchemaVersion: SchemaVersionCurrent,
		Kind:          KindBundle,
		Bundle:        &BundleSpec{Name: "x", Version: "1.0.0", Skills: []BundleSkill{{Slug: "s", Instruction: "i"}}},
		MCP:           &MCPSpec{},
	}
	if err := spec.Validate(); err == nil {
		t.Fatal("expected error for cross-body, got nil")
	}
}

func TestSpec_KindBundle_RejectsMissingBody(t *testing.T) {
	t.Parallel()
	spec := Spec{SchemaVersion: SchemaVersionCurrent, Kind: KindBundle}
	if err := spec.Validate(); err == nil {
		t.Fatal("expected error for nil body, got nil")
	}
}

func TestSpec_UnmarshalJSON_KindBundleEmptyBody(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"schema_version":1,"kind":"bundle"}`)
	var spec Spec
	if err := json.Unmarshal(raw, &spec); err == nil {
		t.Fatal("expected error for empty bundle body, got nil")
	}
}
