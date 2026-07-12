package store

import (
	"context"
	"encoding/json"
	"testing"
)

// TestGetEnabledCapabilitiesForAgent_PinningModeLatestFields exercises
// the SQL change at the heart of MR !309: GetEnabledCapabilitiesForAgent
// must surface BOTH the pinned cv.* columns AND the lateral-joined
// latest_* columns in a single round-trip. The daemon resolver then
// picks one set or the other based on pinning_mode.
//
// The unit tests in connector/agentdaemon stub the store and inject
// hand-built rows; they verify the in-memory switch but NOT that the
// SQL actually returns latest_oss_key / latest_sha256 /
// latest_canonical_spec / latest_version literals correctly. This test
// fills that gap by driving a real Postgres through the store API.
//
// Scenario reproduces the original bug:
//   - capability has v1 with empty oss_key (pre-b77a1c1c markdown era)
//   - capability has v2 with proper oss_key + sha256 + canonical_spec
//   - binding is pinned to v1 (the pre-change state on prod)
//   - row returned by GetEnabledCapabilitiesForAgent should carry:
//   - OssKey == "" (pinned v1)
//   - LatestOssKey == "capabilities/skills/test/v2.zip" (lateral v2)
//
// Skipped when PARSAR_TEST_DATABASE_URL is unset (same convention as
// every other DB-backed test in this package).
func TestGetEnabledCapabilitiesForAgent_PinningModeLatestFields(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ids := DefaultDevFixtureIDs()
	st := New(db)

	mustSeedDevFixture(t, ctx, st)

	const v2SHA = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	canonicalV2, err := json.Marshal(map[string]any{
		"schema_version": 1,
		"kind":           "skill",
		"skill":          map[string]any{"slug": "test-skill", "title": "Fresh", "instruction": "fresh"},
	})
	if err != nil {
		t.Fatalf("marshal canonicalV2: %v", err)
	}

	// 1. Capability with a legacy v1 (markdown era, empty oss_key).
	capability, err := st.CreateCapability(ctx, CreateCapabilityInput{
		WorkspaceID: ids.WorkspaceID,
		CreatorID:   ids.UserID,
		Type:        "skill",
		Name:        "pinning-mode-test-skill",
		Description: "Skill with two versions: legacy v1, fresh v2.",
		InitialVersion: &CreateCapabilityVersionInput{
			Version:   "1.0.0",
			CreatorID: ids.UserID,
			Content:   map[string]any{"kind": "skill"},
			// OssKey + SHA256 intentionally empty to match the
			// markdown-era prod state.
		},
	})
	if err != nil {
		t.Fatalf("CreateCapability: %v", err)
	}
	v1Versions, err := st.ListCapabilityVersions(ctx, capability.ID)
	if err != nil {
		t.Fatalf("ListCapabilityVersions (v1): %v", err)
	}
	if len(v1Versions) != 1 {
		t.Fatalf("expected 1 version after creation, got %d", len(v1Versions))
	}
	v1ID := v1Versions[0].ID

	// 2. Reupload as v2 with full storage breadcrumbs.
	_, err = st.CreateCapabilityVersion(ctx, CreateCapabilityVersionInput{
		CapabilityID:  capability.ID,
		Version:       "2.0.0",
		CreatorID:     ids.UserID,
		Content:       map[string]any{"kind": "skill"},
		CanonicalSpec: canonicalV2,
		OssKey:        "capabilities/skills/test/v2.zip",
		SHA256:        v2SHA,
	})
	if err != nil {
		t.Fatalf("CreateCapabilityVersion v2: %v", err)
	}

	// 3. Create an agent with a binding pinned to v1, the prod-bugged
	// state. CreateAgent's InitialAgentCapabilityInput now carries
	// PinningMode; leave it empty to exercise the normalizePinningMode
	// fallback (which should land us on PinningModePinned, matching
	// the DB column default).
	created, err := st.CreateAgent(ctx, CreateAgentInput{
		WorkspaceID:   ids.WorkspaceID,
		Name:          "Pinning Mode Test Agent (pinned)",
		ConnectorType: "agent_daemon",
		AgentConfig: map[string]any{
			"daemon_mode": "sandbox",
			"agent_kind":  "claude_code",
		},
		InitialCapabilities: []InitialAgentCapabilityInput{{
			CapabilityVersionID: v1ID,
			// PinningMode omitted -> normalizePinningMode -> "pinned".
		}},
		CreatedBy: ids.UserID,
	})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	// 4. The crux: GetEnabledCapabilitiesForAgent returns both columns
	// AND the lateral latest_* fields in one row.
	enabled, err := st.GetEnabledCapabilitiesForAgent(ctx, created.Agent.ID)
	if err != nil {
		t.Fatalf("GetEnabledCapabilitiesForAgent: %v", err)
	}
	if len(enabled) != 1 {
		t.Fatalf("expected 1 enabled capability, got %d: %+v", len(enabled), enabled)
	}
	row := enabled[0]

	// Pinned cv.* columns reflect v1: empty oss_key, empty sha256.
	if row.OssKey != "" {
		t.Errorf("pinned OssKey = %q, want empty (v1 is legacy markdown)", row.OssKey)
	}
	if row.SHA256 != "" {
		t.Errorf("pinned SHA256 = %q, want empty", row.SHA256)
	}
	if row.Version != "1.0.0" {
		t.Errorf("pinned Version = %q, want %q", row.Version, "1.0.0")
	}
	if row.PinningMode != PinningModePinned {
		t.Errorf("PinningMode = %q, want %q (column default)", row.PinningMode, PinningModePinned)
	}

	// Lateral latest_* fields reflect v2 — the lifeline of the MR.
	if row.LatestOssKey != "capabilities/skills/test/v2.zip" {
		t.Errorf("LatestOssKey = %q, want %q", row.LatestOssKey, "capabilities/skills/test/v2.zip")
	}
	if row.LatestSHA256 != v2SHA {
		t.Errorf("LatestSHA256 = %q, want %q", row.LatestSHA256, v2SHA)
	}
	if row.LatestVersion != "2.0.0" {
		t.Errorf("LatestVersion = %q, want %q", row.LatestVersion, "2.0.0")
	}
	if len(row.LatestCanonicalSpec) == 0 {
		t.Errorf("LatestCanonicalSpec is empty, want v2 canonical spec body")
	} else {
		// Sanity-check that the canonical spec we get back matches v2.
		var parsed struct {
			Skill struct {
				Title string `json:"title"`
			} `json:"skill"`
		}
		if err := json.Unmarshal(row.LatestCanonicalSpec, &parsed); err != nil {
			t.Errorf("LatestCanonicalSpec decode: %v", err)
		} else if parsed.Skill.Title != "Fresh" {
			t.Errorf("LatestCanonicalSpec skill.title = %q, want %q", parsed.Skill.Title, "Fresh")
		}
	}

	// 5. Flip the binding to PinningModeLatest and re-fetch. Pinned
	// cv.* still reflect v1 (we didn't rewrite capability_version_id);
	// latest_* still reflect v2; PinningMode is now "latest". The
	// daemon resolver's resolveVersionFields then picks v2 fields.
	if _, err := st.EnableAgentCapability(ctx, created.Agent.ID, v1ID, nil, PinningModeLatest); err != nil {
		t.Fatalf("EnableAgentCapability flip to latest: %v", err)
	}
	enabled, err = st.GetEnabledCapabilitiesForAgent(ctx, created.Agent.ID)
	if err != nil {
		t.Fatalf("GetEnabledCapabilitiesForAgent (post-flip): %v", err)
	}
	if len(enabled) != 1 {
		t.Fatalf("expected 1 enabled capability after flip, got %d", len(enabled))
	}
	row = enabled[0]
	if row.PinningMode != PinningModeLatest {
		t.Errorf("after flip, PinningMode = %q, want %q", row.PinningMode, PinningModeLatest)
	}
	if row.LatestOssKey != "capabilities/skills/test/v2.zip" {
		t.Errorf("after flip, LatestOssKey = %q, want %q", row.LatestOssKey, "capabilities/skills/test/v2.zip")
	}
	if row.Version != "1.0.0" {
		// cv.* still reflects v1 — pinning_mode flip does NOT
		// rewrite capability_version_id (that's the whole point).
		t.Errorf("after flip, pinned Version = %q, want %q (cv.* shouldn't change)", row.Version, "1.0.0")
	}
}

// TestGetEnabledCapabilitiesForAgent_LatestFreezesOnDeprecation covers
// the deprecation-symmetry fix from review: once a capability is
// deprecated, the lateral subquery must freeze at the version that
// existed at deprecation time rather than continue auto-following new
// versions. Mirrors the UpgradeAgentCapability policy
// (`c.deprecated_at is null`) so 'latest' bindings can't sneak past a
// freeze.
func TestGetEnabledCapabilitiesForAgent_LatestFreezesOnDeprecation(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ids := DefaultDevFixtureIDs()
	st := New(db)

	mustSeedDevFixture(t, ctx, st)

	const goodSHA = "1111111111111111111111111111111111111111111111111111111111111111"
	canonicalGood, _ := json.Marshal(map[string]any{
		"schema_version": 1,
		"kind":           "skill",
		"skill":          map[string]any{"slug": "freeze-skill", "title": "Pre", "instruction": "x"},
	})

	capability, err := st.CreateCapability(ctx, CreateCapabilityInput{
		WorkspaceID: ids.WorkspaceID,
		CreatorID:   ids.UserID,
		Type:        "skill",
		Name:        "deprecation-freeze-skill",
		Description: "Skill we deprecate to test latest freezing.",
		InitialVersion: &CreateCapabilityVersionInput{
			Version:       "1.0.0",
			CreatorID:     ids.UserID,
			Content:       map[string]any{"kind": "skill"},
			CanonicalSpec: canonicalGood,
			OssKey:        "capabilities/skills/freeze/v1.zip",
			SHA256:        goodSHA,
		},
	})
	if err != nil {
		t.Fatalf("CreateCapability: %v", err)
	}
	v1Versions, err := st.ListCapabilityVersions(ctx, capability.ID)
	if err != nil {
		t.Fatalf("ListCapabilityVersions: %v", err)
	}
	v1ID := v1Versions[0].ID

	// Bind an agent with pinning_mode=latest, sanity-check pre-deprecation.
	created, err := st.CreateAgent(ctx, CreateAgentInput{
		WorkspaceID:   ids.WorkspaceID,
		Name:          "Deprecation Freeze Agent",
		ConnectorType: "agent_daemon",
		AgentConfig: map[string]any{
			"daemon_mode": "sandbox",
			"agent_kind":  "claude_code",
		},
		InitialCapabilities: []InitialAgentCapabilityInput{{
			CapabilityVersionID: v1ID,
			PinningMode:         PinningModeLatest,
		}},
		CreatedBy: ids.UserID,
	})
	if err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	enabled, err := st.GetEnabledCapabilitiesForAgent(ctx, created.Agent.ID)
	if err != nil {
		t.Fatalf("GetEnabledCapabilitiesForAgent pre-deprecation: %v", err)
	}
	if len(enabled) != 1 || enabled[0].LatestVersion != "1.0.0" {
		t.Fatalf("pre-deprecation latest = %q, want 1.0.0", enabled[0].LatestVersion)
	}

	// Mark capability deprecated.
	if _, err := db.Exec(ctx, `update capability set deprecated_at = now() where id = $1::uuid`, capability.ID); err != nil {
		t.Fatalf("set deprecated_at: %v", err)
	}

	// Publish a new version AFTER deprecation. A naive lateral would
	// still pick this up; the fixed lateral filters by
	// created_at <= deprecated_at and so should ignore v2.
	const newerSHA = "2222222222222222222222222222222222222222222222222222222222222222"
	canonicalNewer, _ := json.Marshal(map[string]any{
		"schema_version": 1,
		"kind":           "skill",
		"skill":          map[string]any{"slug": "freeze-skill", "title": "Post", "instruction": "post"},
	})
	if _, err := st.CreateCapabilityVersion(ctx, CreateCapabilityVersionInput{
		CapabilityID:  capability.ID,
		Version:       "2.0.0",
		CreatorID:     ids.UserID,
		Content:       map[string]any{"kind": "skill"},
		CanonicalSpec: canonicalNewer,
		OssKey:        "capabilities/skills/freeze/v2.zip",
		SHA256:        newerSHA,
	}); err != nil {
		t.Fatalf("CreateCapabilityVersion v2 (post-deprecation): %v", err)
	}

	enabled, err = st.GetEnabledCapabilitiesForAgent(ctx, created.Agent.ID)
	if err != nil {
		t.Fatalf("GetEnabledCapabilitiesForAgent post-deprecation: %v", err)
	}
	if len(enabled) != 1 {
		t.Fatalf("expected 1 enabled, got %d", len(enabled))
	}
	row := enabled[0]
	// Latest should still be v1; the lateral must NOT pick up v2 after
	// deprecation. If this fires, the (deprecated_at is null OR
	// created_at <= deprecated_at) guard in the lateral subquery is
	// missing or broken.
	if row.LatestVersion != "1.0.0" {
		t.Errorf("post-deprecation LatestVersion = %q, want 1.0.0 (frozen at deprecation)", row.LatestVersion)
	}
	if row.LatestOssKey != "capabilities/skills/freeze/v1.zip" {
		t.Errorf("post-deprecation LatestOssKey = %q, want v1 key (frozen at deprecation)", row.LatestOssKey)
	}
}
