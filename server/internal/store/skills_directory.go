package store

import (
	"context"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

func (s *Store) ListSkillsDirectoryInstalls(ctx context.Context, workspaceID string) (map[string]string, error) {
	wid, err := uuid(workspaceID)
	if err != nil {
		return nil, err
	}
	rows, err := sqlc.New(s.db).ListSkillsDirectoryInstalls(ctx, wid)
	if err != nil {
		return nil, err
	}
	installs := make(map[string]string, len(rows))
	for _, row := range rows {
		installs[row.RegistryID] = row.CapabilityID
	}
	return installs, nil
}
