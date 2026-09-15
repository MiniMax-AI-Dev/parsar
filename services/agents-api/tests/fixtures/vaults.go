package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
)

// Synthetic archived rows prove filtering; no public archive writer exists.
func seedVaultList(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var f struct {
		Tenant string   `json:"tenant"`
		Vaults []string `json:"vaults"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return err
	}
	if len(f.Vaults) == 0 {
		return errors.New("Vault fixture IDs required")
	}
	ctx := context.Background()
	pool, err := fixturePool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // Committed transactions cannot roll back.
	result, err := tx.Exec(ctx, "UPDATE vaults SET status='archived' WHERE tenant_id=$1 AND id=ANY($2::uuid[])", f.Tenant, f.Vaults)
	if err != nil {
		return err
	}
	if result.RowsAffected() != int64(len(f.Vaults)) {
		return errors.New("Vault fixture IDs must be unique and owned by the supplied project")
	}
	return tx.Commit(ctx)
}
