package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

var ErrSkillAuthoringConflict = errors.New("Skill changed or was published; read its current version before updating")

func checkAuthoringSkillVersion(ctx context.Context, q *sqlc.Queries, input ImportCapabilityVersionInput) error {
	if input.ExpectedSkillVersionID == "" {
		return nil
	}
	_, err := q.LockAuthoringSkill(ctx, sqlc.LockAuthoringSkillParams{
		ID: mustUUID(input.CapabilityID), WorkspaceID: mustUUID(input.WorkspaceID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrSkillAuthoringConflict
	}
	if err != nil {
		return err
	}
	// The capability lock also serializes publication and version FK inserts.
	latest, err := q.GetLatestCapabilityVersionByCapability(ctx, mustUUID(input.CapabilityID))
	if err != nil {
		return err
	}
	if latest.ID != input.ExpectedSkillVersionID {
		return ErrSkillAuthoringConflict
	}
	return nil
}
