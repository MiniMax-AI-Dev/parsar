package skillcatalog

import (
	"bytes"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
)

func TestBuiltinCatalogLoadsPackages(t *testing.T) {
	loader := New(Options{})
	catalog, err := loader.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(catalog.Items) != 3 {
		t.Fatalf("items = %d, want 3", len(catalog.Items))
	}
	for _, item := range catalog.Items {
		_, pkg, err := loader.LoadItem(item.ID)
		if err != nil {
			t.Fatalf("LoadItem(%q): %v", item.ID, err)
		}
		if pkg.Spec.Kind != canonical.KindSkill || pkg.Spec.Skill == nil {
			t.Fatalf("LoadItem(%q) returned %#v", item.ID, pkg.Spec)
		}
		if len(pkg.Zip) == 0 || len(pkg.SHA256) != 64 {
			t.Fatalf("LoadItem(%q) returned invalid package metadata", item.ID)
		}
		if !bytes.Contains(pkg.Zip, []byte("SKILL.md")) {
			t.Fatalf("LoadItem(%q) zip does not contain SKILL.md", item.ID)
		}
	}
}

func TestCatalogRejectsUnsafeContentPath(t *testing.T) {
	catalog := Catalog{
		SchemaVersion: SchemaVersion,
		UpdatedAt:     "2026-08-03T00:00:00Z",
		Items: []Item{{
			ID:           "demo",
			Name:         "Demo",
			Description:  "Demo skill",
			Publisher:    Publisher{Name: "Publisher", URL: "https://example.com"},
			Categories:   []string{"Testing"},
			FeaturedRank: 1,
			Version:      "1.0.0",
			License:      "MIT",
			SourceRef:    "0000000000000000000000000000000000000000",
			SourcePath:   "skills/demo",
			ContentPath:  "items/other",
		}},
	}
	if err := catalog.Validate(); err == nil {
		t.Fatal("Validate succeeded for mismatched content path")
	}
}
