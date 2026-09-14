package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
)

const executionTransactionTimeout = 5 * time.Second

// ExecutionLease owns the connection used for execution writes, not just election.
// Its gate serializes pgx operations; no daemon or model work holds this gate.
type ExecutionLease struct {
	conn   *pgxpool.Conn
	gate   chan struct{}
	writer Store
}

// AcquireExecutionLease enforces the gateway's single-service ownership per database.
func (s *Store) AcquireExecutionLease(ctx context.Context) (*ExecutionLease, error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	acquired, err := sqlc.New(conn).TryExecutionLease(ctx)
	if err != nil || !acquired {
		_ = conn.Hijack().Close(context.Background())
		if err != nil {
			return nil, err
		}
		return nil, errors.New("another execution service owns this database")
	}
	lease := &ExecutionLease{conn: conn, gate: make(chan struct{}, 1), writer: *s}
	lease.writer.executionLease = lease
	return lease, nil
}

// Store returns the execution writer view. Session transactions use the leased
// connection; reads retain the pool. Keep public admission on the original Store.
// Losing or closing the lease never falls back to a pooled writer connection.
func (l *ExecutionLease) Store() *Store { return &l.writer }

// CheckExecutionOwnership validates the current writer before external preparation.
func (s *Store) CheckExecutionOwnership(ctx context.Context) error {
	if s.executionLease == nil {
		return errors.New("execution operation requires a leased Store")
	}
	ctx, cancel := context.WithTimeout(ctx, executionTransactionTimeout)
	defer cancel()
	return s.executionLease.Ping(ctx)
}

func (l *ExecutionLease) lock(ctx context.Context) error {
	select {
	case l.gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			l.unlock()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *ExecutionLease) unlock() { <-l.gate }

func (l *ExecutionLease) withConn(ctx context.Context, apply func(*pgxpool.Conn) error) error {
	if err := l.lock(ctx); err != nil {
		return err
	}
	defer l.unlock()
	if l.conn == nil {
		return errors.New("execution lease is closed")
	}
	return apply(l.conn)
}

func (l *ExecutionLease) transaction(ctx context.Context, apply func(pgx.Tx) error) error {
	return l.withConn(ctx, func(conn *pgxpool.Conn) error {
		return pgx.BeginFunc(ctx, conn, apply)
	})
}

func (l *ExecutionLease) Ping(ctx context.Context) error {
	return l.withConn(ctx, func(conn *pgxpool.Conn) error { return conn.Ping(ctx) })
}

func (l *ExecutionLease) Close(ctx context.Context) error {
	if err := l.lock(ctx); err != nil {
		return err
	}
	defer l.unlock()
	if l.conn == nil {
		return nil
	}
	conn := l.conn.Hijack()
	l.conn = nil
	return conn.Close(ctx)
}
