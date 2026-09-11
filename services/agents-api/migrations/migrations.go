// Package migrations owns only the Agents API database schema.
package migrations

import (
	"context"
	"database/sql"
	"embed"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed *.sql
var files embed.FS

// Apply upgrades the dedicated execution database without consulting product data.
func Apply(ctx context.Context, databaseURL string) error {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, db, files, goose.WithTableName("agents_api_schema_version"))
	if err != nil {
		return err
	}
	_, err = provider.Up(ctx)
	return err
}
