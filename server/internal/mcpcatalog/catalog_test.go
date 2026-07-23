package mcpcatalog

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestBuiltinCatalogLoads(t *testing.T) {
	snapshot, err := New(Options{}).Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if snapshot.Source != SourceBuiltin || len(snapshot.Catalog.Items) == 0 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	for _, item := range snapshot.Catalog.Items {
		if err := item.CanonicalSpec().Validate(); err != nil {
			t.Fatalf("item %q canonical spec: %v", item.ID, err)
		}
	}
}

func TestBuiltinCatalogContainsCuratedConnectors(t *testing.T) {
	snapshot, err := New(Options{}).Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := map[string]struct {
		version        string
		credentialKind string
	}{
		"context7":        {version: "3.2.3"},
		"exa":             {version: "3.2.1"},
		"firecrawl":       {version: "3.22.4"},
		"postman":         {version: "1.0.0", credentialKind: "postman_mcp_oauth"},
		"notion":          {version: "1.0.0", credentialKind: "notion_mcp_oauth"},
		"sentry":          {version: "1.0.0", credentialKind: "sentry_mcp_oauth"},
		"linear":          {version: "1.0.0", credentialKind: "linear_mcp_oauth"},
		"stripe":          {version: "1.0.0", credentialKind: "stripe_mcp_oauth"},
	}
	if len(snapshot.Catalog.Items) != len(want) {
		t.Fatalf("items=%d, want %d", len(snapshot.Catalog.Items), len(want))
	}
	for _, item := range snapshot.Catalog.Items {
		expected, ok := want[item.ID]
		if !ok {
			t.Fatalf("unexpected connector %q", item.ID)
		}
		if item.Version != expected.version {
			t.Fatalf("connector %q version=%q, want %q", item.ID, item.Version, expected.version)
		}
		if item.Transport != "streamable-http" || !item.Verified {
			t.Fatalf("connector %q has unexpected transport or verification: %+v", item.ID, item)
		}
		if !item.Authentication.ConnectionSupported() {
			t.Fatalf("connector %q is listed but cannot be connected", item.ID)
		}
		if expected.credentialKind != "" {
			if item.Authentication.EffectiveType() != "oauth2" || item.Authentication.CredentialKind != expected.credentialKind {
				t.Fatalf("connector %q authentication = %+v", item.ID, item.Authentication)
			}
			spec := item.CanonicalSpec()
			authorization := spec.MCP.Servers[0].Headers["Authorization"]
			if authorization.Prefix != "Bearer " || authorization.CredentialKindCode != expected.credentialKind {
				t.Fatalf("connector %q authorization header = %+v", item.ID, authorization)
			}
		}
	}
}

func TestCatalogLoadFailsClearlyWhenBuiltinIsInvalid(t *testing.T) {
	_, err := New(Options{BuiltinJSON: []byte(`not-json`)}).Load(context.Background())
	if err == nil || !strings.Contains(err.Error(), "load builtin catalog") {
		t.Fatalf("error = %v", err)
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
		{"transport", func(c *Catalog) { c.Items[0].Transport = "sse" }, "unsupported"},
		{"empty command", func(c *Catalog) { c.Items[0].Server.Command = "" }, "command is required"},
		{"invalid url", func(c *Catalog) { c.Items[0].RepositoryURL = "file:///tmp/mcp" }, "http or https"},
		{"env secret", func(c *Catalog) { c.Items[0].Server.Env = map[string]string{"API_TOKEN": "real-secret"} }, "must not contain a value"},
		{"arg secret", func(c *Catalog) { c.Items[0].Server.Args = []string{"--token=real-secret"} }, "credential value"},
		{"latest package", func(c *Catalog) { c.Items[0].Server.Args = []string{"package@latest"} }, "unpinned latest"},
		{"remote URL", func(c *Catalog) {
			c.Items[0].Transport = "streamable-http"
			c.Items[0].Server = Server{Name: "remote", URL: "file:///tmp/mcp"}
		}, "http or https"},
		{"remote command", func(c *Catalog) {
			c.Items[0].Transport = "streamable-http"
			c.Items[0].Server = Server{Name: "remote", URL: "https://example.com/mcp", Command: "npx"}
		}, "must not set command"},
		{"oauth on stdio", func(c *Catalog) {
			c.Items[0].Authentication = Authentication{Type: "oauth2", CredentialKind: "notion_mcp_oauth"}
		}, "requires streamable-http"},
		{"oauth missing credential kind", func(c *Catalog) {
			c.Items[0].Transport = "streamable-http"
			c.Items[0].Server = Server{Name: "remote", URL: "https://example.com/mcp"}
			c.Items[0].Authentication = Authentication{Type: "oauth2"}
		}, "credential_kind"},
		{"unsupported client registration", func(c *Catalog) {
			c.Items[0].Transport = "streamable-http"
			c.Items[0].Server = Server{Name: "remote", URL: "https://example.com/mcp"}
			c.Items[0].Authentication = Authentication{Type: "oauth2", CredentialKind: "example_mcp_oauth", ClientRegistration: "static"}
		}, "client_registration"},
		{"client registration without oauth", func(c *Catalog) {
			c.Items[0].Authentication = Authentication{Type: "none", ClientRegistration: ClientRegistrationApprovedClient}
		}, "requires oauth2"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			catalog := validCatalog("connector")
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

func validCatalog(id string) Catalog {
	return Catalog{
		SchemaVersion: SchemaVersion,
		UpdatedAt:     "2026-07-22T00:00:00Z",
		Items: []Item{{
			ID:            id,
			Name:          "Connector",
			Description:   "A connector used by tests.",
			Publisher:     Publisher{Name: "Publisher", URL: "https://example.com"},
			RepositoryURL: "https://example.com/repository",
			Verified:      true,
			Categories:    []string{"Developer Tools"},
			FeaturedRank:  1,
			Version:       "1.0.0",
			Transport:     "stdio",
			Server: Server{
				Name:              id,
				Command:           "npx",
				Args:              []string{"-y", "package@1.0.0"},
				Env:               map[string]string{"OPTIONAL_TOKEN": ""},
				StartupTimeoutSec: 30,
			},
		}},
	}
}
