package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// ExpireEnvironmentInputs settles one bounded batch without creating Turn history.
// Only the current execution writer may run this cross-Session maintenance.
func (s *Store) ExpireEnvironmentInputs(ctx context.Context) (int64, error) {
	if s.executionLease == nil {
		return 0, errors.New("Environment input expiry requires an execution lease")
	}
	ctx, cancel := context.WithTimeout(ctx, executionTransactionTimeout)
	defer cancel()
	var expired int64
	err := s.executionLease.transaction(ctx, func(tx pgx.Tx) error {
		var err error
		expired, err = s.queries.WithTx(tx).ExpireDueEnvironmentInputs(ctx)
		return err
	})
	if err != nil {
		return 0, err
	}
	return expired, nil
}
