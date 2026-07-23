package store

import (
	"context"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

// MCPDirectoryInstall identifies the workspace capability created from one
// MCP Directory catalog item.
type MCPDirectoryInstall struct {
	CatalogID    string
	CapabilityID string
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
			CatalogID:    row.CatalogID,
			CapabilityID: row.CapabilityID,
		})
	}
	return installs, nil
}
