package store

import (
	"context"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

// CapabilityDirectoryInstall identifies the workspace capability created from
// one built-in MCP or Skill Directory catalog item.
type CapabilityDirectoryInstall struct {
	CatalogID    string
	CapabilityID string
}

func (s *Store) ListCapabilityDirectoryInstalls(ctx context.Context, workspaceID, capabilityType, sourceFormat string) ([]CapabilityDirectoryInstall, error) {
	wid, err := uuid(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list capability directory installs: workspace_id: %w", err)
	}
	rows, err := sqlc.New(s.db).ListCapabilityDirectoryInstalls(ctx, sqlc.ListCapabilityDirectoryInstallsParams{
		WorkspaceID:    wid,
		CapabilityType: capabilityType,
		SourceFormat:   sourceFormat,
	})
	if err != nil {
		return nil, fmt.Errorf("list capability directory installs: %w", err)
	}
	installs := make([]CapabilityDirectoryInstall, 0, len(rows))
	for _, row := range rows {
		installs = append(installs, CapabilityDirectoryInstall{CatalogID: row.CatalogID, CapabilityID: row.CapabilityID})
	}
	return installs, nil
}
