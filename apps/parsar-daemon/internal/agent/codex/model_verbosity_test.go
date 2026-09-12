package codex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCatalogVerbositySupport(t *testing.T) {
	catalog := []byte(`{"models":[{"slug":"gpt-5","support_verbosity":true},{"slug":"gpt-5-special","support_verbosity":false}]}`)
	for model, want := range map[string]bool{
		"gpt-5": true, "gpt-5.5": true, "provider/gpt-5.5": true,
		"gpt-5-special": false, "provider/gpt-5-special": false,
		"custom-provider-model": false, "": false, "a/b/gpt-5": false, "a b/gpt-5": false,
	} {
		got, err := catalogSupportsVerbosity(catalog, model)
		if err != nil || got != want {
			t.Errorf("%q: got %v, %v; want %v", model, got, err, want)
		}
	}
	if _, err := catalogSupportsVerbosity([]byte(`{"models":false}`), "gpt-5"); err == nil {
		t.Fatal("accepted malformed catalog")
	}
}

func TestPrepareModelVerbosity(t *testing.T) {
	if !SupportsTextVerbosity {
		t.Skip("catalog probe requires Unix")
	}
	t.Setenv("PARSAR_HOME", t.TempDir())
	binary := filepath.Join(t.TempDir(), "codex")
	catalog := `{"models":[{"slug":"known-model","support_verbosity":true,"native_extra":{"keep":true}}]}`
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s' '"+catalog+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSessionPlan("run", "state", "", map[string]any{"model": "known-model", "model_verbosity": "high"})
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Cleanup()
	if err := prepareModelVerbosity(context.Background(), binary, &plan); err != nil {
		t.Fatal(err)
	}
	kv := plan.ExtraConfig[len(plan.ExtraConfig)-1]
	if kv[0] != "model_catalog_json" {
		t.Fatal("catalog was not pinned")
	}
	name := strings.Trim(kv[1], `"`)
	got, err := os.ReadFile(name)
	if err != nil || string(got) != catalog {
		t.Fatalf("catalog changed: %s, %v", got, err)
	}
	plan.Cleanup()
	if _, err := os.Stat(name); !os.IsNotExist(err) {
		t.Fatalf("catalog was not removed: %v", err)
	}
	plan.Model = "custom-provider-model"
	if err := prepareModelVerbosity(context.Background(), binary, &plan); err == nil {
		t.Fatal("accepted model that would ignore verbosity")
	}
	if err := prepareModelVerbosity(context.Background(), "/missing-codex", &plan); err == nil {
		t.Fatal("accepted unreadable catalog")
	}
}
