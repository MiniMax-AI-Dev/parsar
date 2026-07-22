package mcpcatalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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
	want := map[string]string{
		"filesystem":          "2026.7.10",
		"playwright":          "0.0.78",
		"context7":            "3.2.4",
		"fetch":               "2026.7.10",
		"git":                 "2026.7.10",
		"memory":              "2026.7.4",
		"time":                "2026.7.10",
		"sequential-thinking": "2026.7.4",
		"everything":          "2026.7.4",
		"cloudflare-docs":     "0.4.9",
		"microsoft-learn":     "1.0.0",
		"aws-knowledge":       "1.0.0",
		"deepwiki":            "2.14.3",
		"agent-web":           "0.2.1",
		"arxiv":               "1.2.15",
		"pubmed":              "2.9.8",
		"us-weather":          "0.7.2",
		"mdn-search":          "0.1.0",
		"npm-registry":        "0.1.0",
		"docker-hub":          "0.1.0",
		"wikipedia":           "0.1.0",
	}
	if len(snapshot.Catalog.Items) != len(want) {
		t.Fatalf("items=%d, want %d", len(snapshot.Catalog.Items), len(want))
	}
	for _, item := range snapshot.Catalog.Items {
		version, ok := want[item.ID]
		if !ok {
			t.Fatalf("unexpected connector %q", item.ID)
		}
		if item.Version != version {
			t.Fatalf("connector %q version=%q, want %q", item.ID, item.Version, version)
		}
	}
}

func TestRemoteCatalogLoadsAndCaches(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(validCatalogJSON(t, "remote"))
	}))
	defer server.Close()

	loader := New(Options{RemoteURL: server.URL, CacheTTL: time.Minute})
	first, err := loader.Load(context.Background())
	if err != nil {
		t.Fatalf("first Load: %v", err)
	}
	second, err := loader.Load(context.Background())
	if err != nil {
		t.Fatalf("second Load: %v", err)
	}
	if first.Source != SourceRemote || second.Source != SourceRemote || calls.Load() != 1 {
		t.Fatalf("sources=%q/%q calls=%d", first.Source, second.Source, calls.Load())
	}
}

func TestRemoteFailureFallsBackToBuiltin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusBadGateway)
	}))
	defer server.Close()
	snapshot, err := New(Options{RemoteURL: server.URL}).Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if snapshot.Source != SourceBuiltin {
		t.Fatalf("source = %q", snapshot.Source)
	}
}

func TestCatalogLoadFailsClearlyWhenRemoteAndBuiltinAreInvalid(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"schema_version":2}`))
	}))
	defer server.Close()
	_, err := New(Options{RemoteURL: server.URL, BuiltinJSON: []byte(`not-json`)}).Load(context.Background())
	if err == nil || !strings.Contains(err.Error(), "load remote catalog") || !strings.Contains(err.Error(), "load builtin catalog") {
		t.Fatalf("error = %v", err)
	}
}

func TestRemoteCatalogResponseSizeIsBounded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(validCatalogJSON(t, "oversized"))
	}))
	defer server.Close()
	_, err := New(Options{RemoteURL: server.URL, BuiltinJSON: []byte(`not-json`), MaxResponseBytes: 16}).Load(context.Background())
	if err == nil || !strings.Contains(err.Error(), "response exceeds") {
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

func validCatalogJSON(t *testing.T, id string) []byte {
	t.Helper()
	data, err := json.Marshal(validCatalog(id))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func validCatalog(id string) Catalog {
	return Catalog{
		SchemaVersion: SchemaVersion,
		UpdatedAt:     "2026-07-22T00:00:00Z",
		Items: []Item{{
			ID:             id,
			Name:           "Connector",
			Description:    "A connector used by tests.",
			Publisher:      Publisher{Name: "Publisher", URL: "https://example.com"},
			RepositoryURL:  "https://example.com/repository",
			Verified:       true,
			Categories:     []string{"Developer Tools"},
			PopularityRank: 1,
			Version:        "1.0.0",
			Transport:      "stdio",
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
