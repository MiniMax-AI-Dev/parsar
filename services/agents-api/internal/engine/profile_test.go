package engine

import "testing"

func TestCatalogOwnsQualification(t *testing.T) {
	placements := []string{"none"}
	profiles := map[string]Profile{"fixture": {Placements: placements}}
	catalog := NewCatalog(profiles)
	placements[0] = "openai_hosted"
	profiles["fixture"] = Profile{MCPBearer: true}
	profiles["unqualified"] = Profile{Placements: []string{"none"}}
	profile, ok := catalog.Lookup("fixture")
	if !ok || !profile.Accepts("none") || profile.Accepts("openai_hosted") || profile.MCPBearer {
		t.Fatal("caller changed catalog qualification")
	}
	profile.Placements[0] = "self_hosted"
	profile.MCPBearer = true
	again, _ := catalog.Lookup("fixture")
	if !again.Accepts("none") || again.MCPBearer {
		t.Fatal("lookup exposed mutable qualification")
	}
	if _, ok := catalog.Lookup("unqualified"); ok {
		t.Fatal("caller registered an engine after catalog construction")
	}
	if _, ok := (Catalog{}).Lookup("fixture"); ok {
		t.Fatal("fixture widened the service catalog")
	}
}

func TestExplicitCatalogDoesNotInheritBuiltins(t *testing.T) {
	for _, catalog := range []Catalog{NewCatalog(nil), NewCatalog(map[string]Profile{"fixture": {Placements: []string{"none"}}})} {
		if _, ok := catalog.Lookup("codex"); ok {
			t.Fatal("explicit catalog inherited built-in qualification")
		}
	}
	if _, ok := (Catalog{}).Lookup("codex"); !ok {
		t.Fatal("zero-value catalog lost built-in qualification")
	}
}
