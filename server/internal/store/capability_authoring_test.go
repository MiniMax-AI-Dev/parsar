package store

import (
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

func TestSkillAuthoringRejectsChangedDestination(t *testing.T) {
	db := openTestDB(t)
	st := New(db)
	ctx := t.Context()
	mustSeedDevFixture(t, ctx, st)
	ids := DefaultDevFixtureIDs()
	spec := canonical.Spec{SchemaVersion: 1, Kind: canonical.KindSkill, Skill: &canonical.SkillSpec{Slug: "authoring", Instruction: "Original"}}
	created, err := st.ImportCapability(ctx, ImportCapabilityInput{
		WorkspaceID: ids.WorkspaceID, CreatorID: ids.UserID, Type: "skill", Name: "authoring",
		Visibility: "workspace", Version: "1.0.0", Spec: spec,
	})
	if err != nil {
		t.Fatal(err)
	}
	input := ImportCapabilityVersionInput{
		WorkspaceID: ids.WorkspaceID, CreatorID: ids.UserID, CapabilityID: created.Capability.ID,
		Spec: spec, ExpectedSkillVersionID: created.CapabilityVersion.ID,
	}
	updated, err := st.ImportCapabilityVersion(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ImportCapabilityVersion(ctx, input); !errors.Is(err, ErrSkillAuthoringConflict) {
		t.Fatalf("stale version accepted: %v", err)
	}
	input.ExpectedSkillVersionID = updated.CapabilityVersion.ID
	if _, err := db.Exec(ctx, "update capability set visibility = 'public' where id = $1", created.Capability.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ImportCapabilityVersion(ctx, input); !errors.Is(err, ErrSkillAuthoringConflict) {
		t.Fatalf("published destination accepted: %v", err)
	}
	versions, err := st.ListCapabilityVersions(ctx, created.Capability.ID)
	if err != nil || len(versions) != 2 {
		t.Fatalf("rejected operation persisted a version: count=%d, %v", len(versions), err)
	}
	// Ordinary uploads retain their existing publication behavior.
	input.ExpectedSkillVersionID = ""
	if _, err := st.ImportCapabilityVersion(ctx, input); err != nil {
		t.Fatalf("ordinary version upload changed: %v", err)
	}
}

func TestSkillAuthoringLockSerializesPublicationAndVersionInsert(t *testing.T) {
	db := openTestDB(t)
	st := New(db)
	ctx := t.Context()
	mustSeedDevFixture(t, ctx, st)
	ids := DefaultDevFixtureIDs()
	created, err := st.ImportCapability(ctx, ImportCapabilityInput{
		WorkspaceID: ids.WorkspaceID, CreatorID: ids.UserID, Type: "skill", Name: "locked-skill",
		Visibility: "workspace", Version: "1.0.0",
		Spec: canonical.Spec{SchemaVersion: 1, Kind: canonical.KindSkill, Skill: &canonical.SkillSpec{Slug: "locked-skill", Instruction: "Original"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	err = checkAuthoringSkillVersion(ctx, sqlc.New(tx), ImportCapabilityVersionInput{
		CapabilityID: created.Capability.ID, WorkspaceID: ids.WorkspaceID, ExpectedSkillVersionID: created.CapabilityVersion.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		"update capability set visibility = 'public' where id = $1",
		`insert into capability_version (id, capability_id, version, creator_id)
		 select gen_random_uuid(), id, '2.0.0', creator_id from capability where id = $1`,
	} {
		other, err := db.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := other.Exec(ctx, "set local lock_timeout = '100ms'"); err != nil {
			_ = other.Rollback(ctx)
			t.Fatal(err)
		}
		_, err = other.Exec(ctx, query, created.Capability.ID)
		_ = other.Rollback(ctx)
		var state interface{ SQLState() string }
		if !errors.As(err, &state) || state.SQLState() != "55P03" {
			t.Fatalf("concurrent change was not blocked by the capability lock: %v", err)
		}
	}
}
