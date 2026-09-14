package store

import (
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/identity"
	"github.com/jackc/pgx/v5/pgtype"
)

func sessionCreator(kind, id pgtype.Text) (*identity.Subject, error) {
	if !kind.Valid && !id.Valid {
		return nil, nil
	}
	creator := identity.Subject{Kind: kind.String, ID: id.String}
	if !kind.Valid || !id.Valid || creator.Validate() != nil {
		return nil, fmt.Errorf("invalid stored Session creator")
	}
	return &creator, nil
}
