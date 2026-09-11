package store_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/items"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func TestItemOrderMigrationPreservesIndexedHistory(t *testing.T) {
	ctx := context.Background()
	_, pool := store.NewTestStore(t)
	schema := "item_order_" + uuid.New().String()[:8]
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	cfg := pool.Config().ConnConfig.Copy()
	cfg.RuntimeParams["search_path"] = schema
	db := sql.OpenDB(stdlib.GetConnector(*cfg))
	t.Cleanup(func() { _ = db.Close() })
	provider, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS("../../migrations"), goose.WithTableName("agents_api_schema_version"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 10); err != nil {
		t.Fatal(err)
	}
	session, turn, tenant := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err = db.ExecContext(ctx, "INSERT INTO sessions(id,tenant_id,engine,idempotency_key,request_hash) VALUES ($1,$2,'codex','legacy','legacy')", session, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, "INSERT INTO turns(id,session_id) VALUES ($1,$2)", turn, session); err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{
		"00000000-0000-0000-0000-000000000003",
		"00000000-0000-0000-0000-000000000001",
		"00000000-0000-0000-0000-000000000002",
	} {
		payload := `{"type":"function_call_output"}`
		if i == 1 {
			payload = `{"type":"message","role":"user"}`
		}
		if _, err = db.ExecContext(ctx, "INSERT INTO session_items(id,session_id,turn_id,created_at,position,payload) VALUES ($1,$2,$3,'2026-01-01',0,$4)", id, session, turn, payload); err != nil {
			t.Fatal(err)
		}
	}
	readIDs := func() []string {
		t.Helper()
		rows, err := db.QueryContext(ctx, "SELECT id FROM session_items ORDER BY created_at,position,id")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var ids []string
		for rows.Next() {
			var id string
			if err = rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		return ids
	}
	want := readIDs()
	if _, err = provider.UpTo(ctx, 11); err != nil {
		t.Fatal(err)
	}
	if got := readIDs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("migration reordered history: %v; want %v", got, want)
	}
	for i, id := range want {
		var position int
		var output pgtype.Int4
		if err = db.QueryRowContext(ctx, "SELECT position,output_index FROM session_items WHERE id=$1", id).Scan(&position, &output); err != nil {
			t.Fatal(err)
		}
		if position != i || output.Valid != (i > 0) || (output.Valid && int(output.Int32) != i-1) {
			t.Fatalf("migrated item %d: position=%d output=%+v", i, position, output)
		}
	}
	poolConfig := pool.Config()
	poolConfig.ConnConfig = cfg
	migratedPool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(migratedPool.Close)
	s := store.New(migratedPool)
	if _, err = s.TransitionTurn(ctx, tenant, session, turn, store.TurnTransition{ExpectedStatus: store.TurnQueued, Status: store.TurnInProgress}); err != nil {
		t.Fatal(err)
	}
	if err = s.AppendTurnEvents(ctx, tenant, session, turn, 1, []store.ExecutionEvent{{Kind: "delta", Payload: json.RawMessage(`{"item_id":"after-upgrade","delta":"continued"}`)}}); err != nil {
		t.Fatal(err)
	}
	addedID := items.Identity(turn, "message:after-upgrade")
	var position, output int
	if err = db.QueryRowContext(ctx, "SELECT position,output_index FROM session_items WHERE id=$1", addedID).Scan(&position, &output); err != nil || position != 3 || output != 2 {
		t.Fatalf("post-upgrade append: position=%d output=%d err=%v", position, output, err)
	}
	want = append(want, addedID)
	if _, err = provider.Down(ctx); err != nil {
		t.Fatal(err)
	}
	if got := readIDs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("downgrade reordered history: %v", got)
	}
	if _, err = provider.UpTo(ctx, 11); err != nil {
		t.Fatal(err)
	}
}
