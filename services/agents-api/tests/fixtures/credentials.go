package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
)

// Synthetic archived rows prove filtering; no public archive writer exists.
func seedCredentialList(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var f struct {
		Tenant      string   `json:"tenant"`
		Vault       string   `json:"vault"`
		Credentials []string `json:"credentials"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return err
	}
	if len(f.Credentials) == 0 {
		return errors.New("Credential fixture IDs required")
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
	result, err := tx.Exec(ctx, `UPDATE vault_credentials c SET status='archived'
FROM vaults v WHERE v.id=c.vault_id AND v.tenant_id=$1 AND v.id=$2
AND c.id=ANY($3::uuid[])`, f.Tenant, f.Vault, f.Credentials)
	if err != nil {
		return err
	}
	if result.RowsAffected() != int64(len(f.Credentials)) {
		return errors.New("Credential fixture IDs must be unique and owned by the supplied project and Vault")
	}
	return tx.Commit(ctx)
}
