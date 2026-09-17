package store

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/google/uuid"
)

type fileWriteFixture struct {
	s, writer *Store
	lease     *ExecutionLease
	tenant    string
	session   Session
	env       Environment
	key       FileWriteIdentity
}

func newFileWriteFixture(t *testing.T) fileWriteFixture {
	t.Helper()
	s, _ := testStore(t)
	lease := executionLease(t, s)
	tenant := uuid.NewString()
	session, env := localEnvironment(t, s, tenant)
	host, err := s.CreateEnvironmentDevice(t.Context(), tenant, env.ID, "file owner", device.HashCredential(uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	return fileWriteFixture{s: s, writer: lease.Store(), lease: lease, tenant: tenant, session: session, env: env,
		key: FileWriteIdentity{ID: uuid.NewString(), DeviceID: host.ID, RequestSHA256: strings.Repeat("a", 64)}}
}

func TestEnvironmentFileWriteRetainsUnknownAcrossLeaseLoss(t *testing.T) {
	f := newFileWriteFixture(t)
	ctx := t.Context()
	first, err := f.writer.ReserveEnvironmentFileWrite(ctx, f.tenant, f.env.ID, f.key)
	if err != nil || first.Replayed || first.State != "pending" {
		t.Fatal(first, err)
	}
	var killed bool
	if err := f.s.pool.QueryRow(ctx, "SELECT pg_terminate_backend($1, 1000)", f.lease.conn.Conn().PgConn().PID()).Scan(&killed); err != nil || !killed {
		t.Fatal(killed, err)
	}
	if _, err := f.writer.SettleEnvironmentFileWrite(ctx, f.tenant, f.env.ID, f.key, "committed"); err == nil {
		t.Fatal("lost writer settled an upload")
	}
	reopened, _ := testStore(t)
	next := executionLease(t, reopened).Store()
	got, err := next.ReserveEnvironmentFileWrite(ctx, f.tenant, f.env.ID, f.key)
	if err != nil || !got.Replayed || got.State != "pending" || !got.CreatedAt.Equal(first.CreatedAt) || got.Identity != f.key {
		t.Fatal("restart lost unknown write identity", got, err)
	}
	another := f.key
	another.ID = uuid.NewString()
	if _, err := next.ReserveEnvironmentFileWrite(ctx, f.tenant, f.env.ID, another); !errors.Is(err, ErrTurnConflict) {
		t.Fatal("restart admitted successor", err)
	}
	if _, err := reopened.ReserveEnvironmentInput(ctx, f.tenant, f.session.ID, "new-input", []Input{messageInput("new")}); !errors.Is(err, ErrTurnConflict) {
		t.Fatal("unknown write admitted input", err)
	}
	if _, err := reopened.SubmitMessage(ctx, f.tenant, f.session.ID, "direct", messageInput("new").Payload); !errors.Is(err, ErrTurnConflict) {
		t.Fatal("direct admission bypassed write", err)
	}
	if _, err := reopened.GetEnvironment(ctx, f.tenant, f.env.ID); err != nil {
		t.Fatal("write gate prevented metadata read", err)
	}
	if _, err := reopened.GetSession(ctx, f.tenant, f.session.ID); err != nil {
		t.Fatal("write gate prevented recovery read", err)
	}
	if receipt, err := reopened.RequestCancel(ctx, f.tenant, f.session.ID, "idle-cancel"); err != nil || receipt.TurnID != "" {
		t.Fatal("write gate imposed mutation admission on idle cancellation", receipt, err)
	}
	if got, err := reopened.GetEnvironmentFileWrite(ctx, f.tenant, f.env.ID, f.key.ID); err != nil || got.State != "pending" {
		t.Fatal("idle cancellation cleared unknown write", got, err)
	}
	settled, err := next.SettleEnvironmentFileWrite(ctx, f.tenant, f.env.ID, f.key, "committed")
	if err != nil || settled.State != "committed" || settled.SettledAt == nil {
		t.Fatal(settled, err)
	}
	reserveEnvironmentInput(t, reopened, f.tenant, f.session.ID, "after-commit")
}

func TestEnvironmentFileWriteMatchesReceiptAndRetainsDeletedOwner(t *testing.T) {
	f := newFileWriteFixture(t)
	ctx := t.Context()
	if _, err := f.s.ReserveEnvironmentFileWrite(ctx, f.tenant, f.env.ID, f.key); err == nil {
		t.Fatal("pooled Store admitted a file write")
	}
	if _, err := f.writer.ReserveEnvironmentFileWrite(ctx, f.tenant, f.env.ID, f.key); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*FileWriteIdentity){
		func(k *FileWriteIdentity) { k.DeviceID = uuid.NewString() },
		func(k *FileWriteIdentity) { k.RequestSHA256 = strings.Repeat("b", 64) },
	} {
		wrong := f.key
		change(&wrong)
		if _, err := f.writer.ReserveEnvironmentFileWrite(ctx, f.tenant, f.env.ID, wrong); !errors.Is(err, ErrIdempotencyConflict) {
			t.Fatal("changed retry accepted", err)
		}
		if _, err := f.writer.SettleEnvironmentFileWrite(ctx, f.tenant, f.env.ID, wrong, "rejected"); !errors.Is(err, ErrIdempotencyConflict) {
			t.Fatal("mismatched receipt settled", err)
		}
	}
	if _, err := f.writer.SettleEnvironmentFileWrite(ctx, uuid.NewString(), f.env.ID, f.key, "committed"); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-tenant settlement", err)
	}
	if _, err := f.s.GetEnvironmentFileWrite(ctx, uuid.NewString(), f.env.ID, f.key.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-tenant read", err)
	}
	for _, state := range []string{"pending", "unknown", "cancelled", "retired"} {
		if _, err := f.writer.SettleEnvironmentFileWrite(ctx, f.tenant, f.env.ID, f.key, state); !errors.Is(err, ErrInvalidInput) {
			t.Fatal("non-receipt settled write", state, err)
		}
	}
	if err := f.s.DeleteSession(ctx, f.tenant, f.session.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := f.s.GetEnvironmentFileWrite(ctx, f.tenant, f.env.ID, f.key.ID); err != nil || got.State != "pending" {
		t.Fatal("deletion discarded unresolved write", got, err)
	}
	if _, err := f.writer.ReserveEnvironmentFileWrite(ctx, f.tenant, f.env.ID, f.key); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted Session reopened write", err)
	}
	if _, err := f.writer.SettleEnvironmentFileWrite(ctx, f.tenant, f.env.ID, f.key, "rejected"); err != nil {
		t.Fatal("cannot settle retained deleted owner", err)
	}
	if got, err := f.writer.SettleEnvironmentFileWrite(ctx, f.tenant, f.env.ID, f.key, "rejected"); err != nil || !got.Replayed {
		t.Fatal("receipt retry lost identity", got, err)
	}
	if _, err := f.writer.SettleEnvironmentFileWrite(ctx, f.tenant, f.env.ID, f.key, "committed"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatal("terminal outcome changed", err)
	}
}

func TestEnvironmentFileWriteSerializesWithInputAndRetry(t *testing.T) {
	f := newFileWriteFixture(t)
	original := f.key
	ctx := t.Context()
	var group sync.WaitGroup
	results := make(chan EnvironmentFileWrite, 8)
	for range 8 {
		group.Go(func() {
			got, err := f.writer.ReserveEnvironmentFileWrite(ctx, f.tenant, f.env.ID, f.key)
			if err != nil {
				t.Error(err)
				return
			}
			results <- got
		})
	}
	group.Wait()
	close(results)
	fresh, total := 0, 0
	for got := range results {
		total++
		if !got.Replayed {
			fresh++
		}
	}
	if total != 8 || fresh != 1 {
		t.Fatal("retry authorized multiple sends", total, fresh)
	}
	if _, err := f.writer.SettleEnvironmentFileWrite(ctx, f.tenant, f.env.ID, f.key, "committed"); err != nil {
		t.Fatal(err)
	}
	for range 6 {
		f.key.ID = uuid.NewString()
		start := make(chan struct{})
		writes, inputs := make(chan error, 1), make(chan error, 1)
		var pending EnvironmentInputReservation
		group.Go(func() {
			<-start
			_, err := f.writer.ReserveEnvironmentFileWrite(ctx, f.tenant, f.env.ID, f.key)
			writes <- err
		})
		inputKey := uuid.NewString()
		group.Go(func() {
			<-start
			var err error
			pending, err = f.s.ReserveEnvironmentInput(ctx, f.tenant, f.session.ID, inputKey, []Input{messageInput("race")})
			inputs <- err
		})
		close(start)
		group.Wait()
		writeErr, inputErr := <-writes, <-inputs
		if writeErr == nil && errors.Is(inputErr, ErrTurnConflict) {
			if _, err := f.writer.SettleEnvironmentFileWrite(ctx, f.tenant, f.env.ID, f.key, "rejected"); err != nil {
				t.Fatal(err)
			}
		} else if inputErr == nil && errors.Is(writeErr, ErrTurnConflict) {
			if _, err := f.s.CancelEnvironmentInput(ctx, f.tenant, f.session.ID, pending.ID); err != nil {
				t.Fatal(err)
			}
		} else {
			t.Fatal("write/input did not serialize", writeErr, inputErr)
		}
	}
	f.key.ID = uuid.NewString()
	if _, err := f.writer.ReserveEnvironmentFileWrite(ctx, f.tenant, f.env.ID, f.key); err != nil {
		t.Fatal(err)
	}
	if got, err := f.writer.SettleEnvironmentFileWrite(ctx, f.tenant, f.env.ID, original, "committed"); err != nil || !got.Replayed {
		t.Fatal("old receipt retry changed", got, err)
	}
	if got, err := f.s.GetEnvironmentFileWrite(ctx, f.tenant, f.env.ID, f.key.ID); err != nil || got.State != "pending" {
		t.Fatal("old receipt cleared successor", got, err)
	}
}
