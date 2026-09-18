package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/google/uuid"
)

func TestRuntimeAllocationAtomicOwnershipAndRecovery(t *testing.T) {
	s, pool := testStore(t)
	lease := executionLease(t, s)
	w := lease.Store()
	tenant, provider := uuid.NewString(), uuid.NewString()
	session, environment := localEnvironment(t, s, tenant)
	secret := uuid.NewString()
	owner, err := w.ReserveRuntimeAllocation(t.Context(), tenant, environment.ID, provider, device.HashCredential(secret))
	if err != nil || owner.Replayed || owner.State != "creating" || owner.CreateSettled {
		t.Fatalf("reservation: %+v %v", owner, err)
	}
	bound, err := s.GetSessionDevice(t.Context(), tenant, session.ID)
	if err != nil || bound.ID != owner.DeviceID || bound.EnvironmentID != environment.ID {
		t.Fatalf("binding not committed with allocation: %+v %v", bound, err)
	}
	if _, err := w.ReserveRuntimeAllocation(t.Context(), uuid.NewString(), environment.ID, provider, device.HashCredential(secret)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign allocation accepted: %v", err)
	}
	if _, err := w.ReserveRuntimeAllocation(t.Context(), tenant, environment.ID, uuid.NewString(), device.HashCredential(secret)); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("provider target changed: %v", err)
	}
	if err := lease.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := w.ObserveRuntimeRunning(t.Context(), owner); err == nil {
		t.Fatal("lost writer changed allocation")
	}
	reopened, _ := testStore(t)
	next := executionLease(t, reopened).Store()
	retry, err := next.ReserveRuntimeAllocation(t.Context(), tenant, environment.ID, provider, device.HashCredential(uuid.NewString()))
	if err != nil || !retry.Replayed || retry.ID != owner.ID || retry.DeviceID != owner.DeviceID {
		t.Fatalf("restart replaced unknown allocation: %+v %v", retry, err)
	}
	credential, ok, err := s.GetDeviceCredential(t.Context(), owner.DeviceID)
	if err != nil || !ok || credential.CredentialHash != device.HashCredential(secret) {
		t.Fatal("retry rewrote bootstrap credential")
	}
	if err := s.DeleteSession(t.Context(), tenant, session.ID); err != nil {
		t.Fatal(err)
	}
	retained, err := next.GetRuntimeAllocation(t.Context(), tenant, environment.ID)
	if err != nil || !retained.SessionDeleted || retained.ID != owner.ID {
		t.Fatalf("deletion discarded cleanup identity: %+v %v", retained, err)
	}
	if _, err := next.ObserveRuntimeRunning(t.Context(), owner); !errors.Is(err, ErrNotFound) {
		t.Fatalf("late creation revived deleted Session: %v", err)
	}
	if _, err := next.RequestRuntimeCleanup(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	if _, err := next.ReleaseRuntimeAllocation(t.Context(), owner); !errors.Is(err, ErrTurnConflict) {
		t.Fatalf("unknown Create forgotten: %v", err)
	}
	found, cursor := false, ""
	for !found {
		rows, err := next.ListRuntimeAllocations(t.Context(), cursor)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			found = found || row.ID == owner.ID
			cursor = row.ID
		}
	}

	if !found {
		t.Fatal("unknown cleanup absent from recovery scan")
	}
	if _, err := next.SettleRuntimeCreation(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	if _, err := next.ReleaseRuntimeAllocation(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	var releases int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM runtime_allocations WHERE id=$1 AND released_at IS NOT NULL", owner.ID).Scan(&releases); err != nil || releases != 1 {
		t.Fatalf("cleanup confirmation not durable: %d %v", releases, err)
	}
}

func TestRuntimeAllocationOneWinnerAndRollback(t *testing.T) {
	s, pool := testStore(t)
	w := executionLease(t, s).Store()
	tenant, provider := uuid.NewString(), uuid.NewString()
	_, environment := localEnvironment(t, s, tenant)
	var wg sync.WaitGroup
	results := make(chan RuntimeAllocation, 8)
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value, err := w.ReserveRuntimeAllocation(t.Context(), tenant, environment.ID, provider, device.HashCredential(uuid.NewString()))
			results <- value
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	winners, id := 0, ""
	for result := range results {
		if !result.Replayed {
			winners++
		}
		if id != "" && id != result.ID {
			t.Fatal("multiple allocation identities")
		}
		id = result.ID
	}
	if winners != 1 {
		t.Fatalf("%d fresh Create receipts", winners)
	}
	_, fail := localEnvironment(t, s, tenant)
	// Reject the final insert after device/binding writes to prove transactional rollback.
	constraint := "fixture_" + uuid.NewString()[:8]
	_, err := pool.Exec(t.Context(), "ALTER TABLE runtime_allocations ADD CONSTRAINT "+constraint+" CHECK (environment_id <> '"+fail.ID+"'::uuid)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "ALTER TABLE runtime_allocations DROP CONSTRAINT "+constraint)
	})
	if _, err := w.ReserveRuntimeAllocation(t.Context(), tenant, fail.ID, provider, device.HashCredential(uuid.NewString())); err == nil {
		t.Fatal("injected insert failure succeeded")
	}
	var count int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM devices WHERE environment_id=$1", fail.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed reservation left a device: %d %v", count, err)
	}
}

func TestRuntimeAllocationExpiryAndRevocation(t *testing.T) {
	s, pool := testStore(t)
	w := executionLease(t, s).Store()
	tenant := uuid.NewString()
	_, environment := localEnvironment(t, s, tenant)
	owner, err := w.ReserveRuntimeAllocation(t.Context(), tenant, environment.ID, uuid.NewString(), device.HashCredential(uuid.NewString()))
	if err != nil {
		t.Fatal(err)
	}
	owner, err = w.ObserveRuntimeRunning(t.Context(), owner)
	if err != nil || !owner.CreateSettled {
		t.Fatalf("running observation: %+v %v", owner, err)
	}
	if _, err := w.KeepRuntimeAllocation(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "UPDATE runtime_allocations SET kept_at=clock_timestamp()-interval '61 minutes' WHERE id=$1", owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.KeepRuntimeAllocation(t.Context(), owner); !errors.Is(err, ErrTurnConflict) {
		t.Fatalf("expired allocation renewed: %v", err)
	}
	if _, err := w.RequestRuntimeCleanup(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.GetDeviceCredential(t.Context(), owner.DeviceID); err != nil || ok {
		t.Fatal("cleanup credential still authenticates")
	}
	if _, err := w.ReleaseRuntimeAllocation(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	got, err := w.ReserveRuntimeAllocation(t.Context(), tenant, environment.ID, owner.ProviderKey, device.HashCredential(uuid.NewString()))
	if err != nil || !got.Replayed || got.State != "released" || got.KeptAt.After(time.Now()) {
		t.Fatalf("cleanup permitted replacement: %+v %v", got, err)
	}
}
