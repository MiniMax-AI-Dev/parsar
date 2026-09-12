package codex

import "slices"

func disableSubagents(plan *SessionPlan) {
	// Native v1 and v2 controls must override operator feature preferences.
	for _, feature := range []string{"multi_agent", "multi_agent_v2"} {
		plan.EnableFeatures = slices.DeleteFunc(plan.EnableFeatures, func(value string) bool { return value == feature })
		if !slices.Contains(plan.DisableFeatures, feature) {
			plan.DisableFeatures = append(plan.DisableFeatures, feature)
		}
	}
}
