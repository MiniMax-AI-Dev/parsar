package codex

import (
	"reflect"
	"testing"
)

func TestWebSearchConfiguration(t *testing.T) {
	for _, mode := range []string{"", "disabled", "cached", "live"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("PARSAR_HOME", t.TempDir())
			opts := map[string]any{}
			var want [][2]string
			if mode != "" {
				opts["web_search"] = mode
				want = [][2]string{{"web_search", `"` + mode + `"`}}
			}
			plan, err := BuildSessionPlan("run", "state", "", opts)
			if err != nil {
				t.Fatal(err)
			}
			defer plan.Cleanup()
			if !reflect.DeepEqual(plan.ExtraConfig, want) {
				t.Fatalf("config = %v, want %v", plan.ExtraConfig, want)
			}
		})
	}
	for _, value := range []any{nil, "", "enabled", true, 1, map[string]any{}, []any{}} {
		if _, err := BuildSessionPlan("run", "state", "", map[string]any{"web_search": value}); err == nil {
			t.Fatalf("accepted invalid web_search %T", value)
		}
	}
}
