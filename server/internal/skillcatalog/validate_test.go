package skillcatalog

import (
	"encoding/json"
	"strings"
	"testing"
)

func validCatalogItem(id string) Item {
	return Item{
		ID:           id,
		Name:         "Example Skill",
		Description:  "A reviewed example skill.",
		Publisher:    Publisher{Name: "Example", URL: "https://example.com"},
		Categories:   []string{"Testing"},
		FeaturedRank: 1,
		Version:      "1.0.0",
		License:      "MIT",
		SourceRef:    strings.Repeat("a", 40),
		SourcePath:   "skills/example",
		ContentPath:  "items/" + id,
	}
}

func TestCatalogRejectsDuplicateIDs(t *testing.T) {
	first := validCatalogItem("example")
	second := validCatalogItem("example")
	if err := (Catalog{SchemaVersion: SchemaVersion, UpdatedAt: "2026-08-03T00:00:00Z", Items: []Item{first, second}}).Validate(); err == nil {
		t.Fatal("Validate accepted duplicate catalog IDs")
	}
}

func TestCatalogRejectsInvalidURL(t *testing.T) {
	item := validCatalogItem("example")
	item.RepositoryURL = "http://example.com/repository"
	if err := item.Validate(); err == nil {
		t.Fatal("Validate accepted a non-HTTPS repository URL")
	}
}

func TestCatalogRejectsUnsupportedSchemaVersion(t *testing.T) {
	data, err := json.Marshal(Catalog{SchemaVersion: SchemaVersion + 1, UpdatedAt: "2026-08-03T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(data); err == nil {
		t.Fatal("Decode accepted an unsupported schema version")
	}
}

func TestCatalogRejectsSourcePathTraversal(t *testing.T) {
	item := validCatalogItem("example")
	item.SourcePath = "skills/../example"
	if err := item.Validate(); err == nil {
		t.Fatal("Validate accepted a source path containing ..")
	}
}
