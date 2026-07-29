package store

import (
	"context"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

// MCPDirectoryInstall identifies the workspace capability created from one
// MCP Directory catalog item. CatalogVersion is retained for future update
// detection; v1 only reports it.
type MCPDirectoryInstall struct {
	CatalogID      string `json:"catalog_id"`
	CatalogVersion string `json:"catalog_version"`
	CapabilityID   string `json:"capability_id"`
}

func (s *Store) ListMCPDirectoryInstalls(ctx context.Context, workspaceID string) ([]MCPDirectoryInstall, error) {
	wid, err := uuid(workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list mcp directory installs: workspace_id: %w", err)
	}
	rows, err := sqlc.New(s.db).ListMCPDirectoryInstalls(ctx, wid)
	if err != nil {
		return nil, fmt.Errorf("list mcp directory installs: %w", err)
	}
	installs := make([]MCPDirectoryInstall, 0, len(rows))
	for _, row := range rows {
		installs = append(installs, MCPDirectoryInstall{
			CatalogID:      row.CatalogID,
			CatalogVersion: row.CatalogVersion,
			CapabilityID:   row.CapabilityID,
		})
	}
	return installs, nil
}
