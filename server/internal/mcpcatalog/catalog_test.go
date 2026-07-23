package mcpcatalog

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuiltinCatalogLoads(t *testing.T) {
	catalog, err := New(Options{}).Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"context7", "exa", "firecrawl", "notion"}
	if len(catalog.Items) != len(want) {
		t.Fatalf("items = %d, want %d", len(catalog.Items), len(want))
	}
	for index, id := range want {
		item := catalog.Items[index]
		if item.ID != id {
			t.Fatalf("item[%d] = %q, want %q", index, item.ID, id)
		}
		if err := item.CanonicalSpec().Validate(); err != nil {
			t.Fatalf("item %q canonical spec: %v", item.ID, err)
		}
		if item.ID == "notion" {
			header := item.CanonicalSpec().MCP.Servers[0].Headers["Authorization"]
			if header.Prefix != "Bearer " || header.CredentialKindCode != OAuthCredentialKind {
				t.Fatalf("notion authorization header = %+v", header)
			}
		}
	}
}

func TestCatalogValidationRejectsInvalidContent(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Catalog)
		want string
	}{
		{"schema version", func(c *Catalog) { c.SchemaVersion = 2 }, "schema_version"},
		{"duplicate id", func(c *Catalog) { c.Items = append(c.Items, c.Items[0]) }, "duplicated"},
		{"transport", func(c *Catalog) { c.Items[0].Transport = "stdio" }, "unsupported"},
		{"insecure URL", func(c *Catalog) { c.Items[0].Server.URL = "http://example.com/mcp" }, "https URL"},
		{"embedded credentials", func(c *Catalog) { c.Items[0].Server.URL = "https://token@example.com/mcp" }, "embedded credentials"},
		{"featured rank", func(c *Catalog) { c.Items[0].FeaturedRank = 0 }, "featured_rank"},
		{"authentication type", func(c *Catalog) {
			c.Items[0].Authentication = Authentication{Type: "api_key"}
		}, "unsupported"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			catalog := validCatalog()
			tc.edit(&catalog)
			data, err := json.Marshal(catalog)
			if err != nil {
				t.Fatal(err)
			}
			_, err = Decode(data)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func validCatalog() Catalog {
	return Catalog{
		SchemaVersion: SchemaVersion,
		UpdatedAt:     "2026-07-23T00:00:00Z",
		Items: []Item{{
			ID:            "connector",
			Name:          "Connector",
			Description:   "A connector used by tests.",
			Publisher:     Publisher{Name: "Publisher", URL: "https://example.com"},
			RepositoryURL: "https://example.com/repository",
			Verified:      true,
			Categories:    []string{"Developer Tools"},
			FeaturedRank:  1,
			Version:       "1.0.0",
			Transport:     "streamable-http",
			Server:        Server{Name: "connector", URL: "https://example.com/mcp"},
		}},
	}
}
