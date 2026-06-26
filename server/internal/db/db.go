package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

const DefaultDatabaseName = "parsar_dev"

const DefaultDatabaseURL = "postgres://parsar:parsar@127.0.0.1:15432/parsar_dev?sslmode=disable"

func OpenPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	if databaseURL == "" {
		databaseURL = DefaultDatabaseURL
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}
