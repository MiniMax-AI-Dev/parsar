package codex

import (
	"reflect"
	"testing"
)

func TestDisableSubagentsOverridesNativeFeaturePreferences(t *testing.T) {
	plan := SessionPlan{
		EnableFeatures:  []string{"multi_agent", "unrelated", "multi_agent_v2"},
		DisableFeatures: []string{"another", "multi_agent"},
	}
	disableSubagents(&plan)
	if !reflect.DeepEqual(plan.EnableFeatures, []string{"unrelated"}) ||
		!reflect.DeepEqual(plan.DisableFeatures, []string{"another", "multi_agent", "multi_agent_v2"}) {
		t.Fatal(plan.EnableFeatures, plan.DisableFeatures)
	}
}
