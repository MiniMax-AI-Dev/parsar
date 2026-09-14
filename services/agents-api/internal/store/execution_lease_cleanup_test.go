package store

import (
	"context"
	"errors"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestExecutionLeaseCloseWaitsForCancelledConnectionCleanup(t *testing.T) {
	dsn := os.Getenv("PARSAR_AGENTS_API_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("dedicated PostgreSQL required")
	}
	cfg, err := testDatabaseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	observer, err := pgxpool.NewWithConfig(t.Context(), cfg.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(observer.Close)
	var armed atomic.Bool
	blocked, release := make(chan struct{}), make(chan struct{})
	var signal, unblocked sync.Once
	unblock := func() { unblocked.Do(func() { close(release) }) }
	dial := cfg.ConnConfig.DialFunc
	cfg.ConnConfig.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
		if armed.Load() {
			signal.Do(func() { close(blocked) })
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return dial(ctx, network, address)
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	lease, err := New(pool).AcquireExecutionLease(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		unblock()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := lease.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	connection := lease.conn.Conn().PgConn()
	armed.Store(true)
	queryCtx, cancelQuery := context.WithCancel(t.Context())
	defer cancelQuery()
	queryDone := make(chan error, 1)
	go func() {
		queryDone <- lease.withConn(queryCtx, func(conn *pgxpool.Conn) error {
			_, err := conn.Exec(queryCtx, "SELECT pg_sleep(10)")
			return err
		})
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		var sleeping bool
		err := observer.QueryRow(t.Context(), "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND wait_event='PgSleep')", connection.PID()).Scan(&sleeping)
		if err != nil {
			t.Fatal(err)
		}
		if sleeping {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("owner query did not reach PostgreSQL")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancelQuery()
	if err := <-queryDone; err == nil || !connection.IsClosed() {
		t.Fatal("cancelled query did not close the driver connection", err)
	}
	select {
	case <-blocked:
	case <-time.After(2 * time.Second):
		t.Fatal("asynchronous cancellation did not reach the dial gate")
	}
	closeCtx, cancelClose := context.WithTimeout(t.Context(), 25*time.Millisecond)
	err = lease.Close(closeCtx)
	cancelClose()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("Close returned before blocked cleanup or ignored its deadline", err)
	}
	if err := lease.Store().CheckExecutionOwnership(t.Context()); err == nil {
		t.Fatal("timed-out cleanup restored writer authority")
	}
	closed := make(chan error, 1)
	go func() { closed <- lease.Close(t.Context()) }()
	select {
	case err := <-closed:
		t.Fatal("repeated Close forgot pending cleanup", err)
	case <-time.After(25 * time.Millisecond):
	}
	unblock()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not finish after cleanup resumed")
	}
	select {
	case <-connection.CleanupDone():
	default:
		t.Fatal("successful Close left driver cleanup pending")
	}
}
