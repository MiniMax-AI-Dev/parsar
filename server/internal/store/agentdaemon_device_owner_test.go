package store

import (
	"context"
	"testing"
	"time"
)

func TestAgentDaemonDeviceOwnerGenerationFencesStalePod(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ids := DefaultDevFixtureIDs()
	st := New(db)
	if _, err := st.InsertDevFixture(ctx, ids); err != nil {
		t.Fatal(err)
	}

	pair, err := st.CreateRuntimePairing(ctx, CreateRuntimePairingInput{
		WorkspaceID: ids.WorkspaceID,
		Type:        "agent_daemon",
		Provider:    RuntimeProviderAgentDaemonSandbox,
		Name:        "owner-test-device-" + t.Name(),
		TokenTTL:    time.Minute,
	})
	if err != nil {
		t.Fatalf("CreateRuntimePairing: %v", err)
	}
	deviceID := pair.Runtime.ID
	t.Cleanup(func() {
		_, _ = db.Exec(ctx, `delete from agent_daemon_device_owners where device_id = $1::uuid`, deviceID)
		_, _ = db.Exec(ctx, `delete from runtimes where id = $1::uuid`, deviceID)
	})

	now := time.Now().UTC()
	first, err := st.ClaimAgentDaemonDeviceOwner(ctx, ClaimAgentDaemonDeviceOwnerInput{
		DeviceID:       deviceID,
		WorkspaceID:    ids.WorkspaceID,
		OwnerPodID:     "pod-a",
		OwnerURL:       "http://pod-a",
		Now:            now,
		LeaseExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if first.Generation != 1 || first.OwnerPodID != "pod-a" {
		t.Fatalf("first owner = %+v, want pod-a generation 1", first)
	}

	second, err := st.ClaimAgentDaemonDeviceOwner(ctx, ClaimAgentDaemonDeviceOwnerInput{
		DeviceID:       deviceID,
		WorkspaceID:    ids.WorkspaceID,
		OwnerPodID:     "pod-b",
		OwnerURL:       "http://pod-b",
		Now:            now.Add(time.Second),
		LeaseExpiresAt: now.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if second.Generation != 2 || second.OwnerPodID != "pod-b" {
		t.Fatalf("second owner = %+v, want pod-b generation 2", second)
	}

	if _, ok, err := st.RenewAgentDaemonDeviceOwner(ctx, RenewAgentDaemonDeviceOwnerInput{
		DeviceID:       deviceID,
		OwnerPodID:     "pod-a",
		Generation:     first.Generation,
		Now:            now.Add(3 * time.Second),
		LeaseExpiresAt: now.Add(3 * time.Minute),
	}); err != nil {
		t.Fatalf("stale renew: %v", err)
	} else if ok {
		t.Fatal("stale generation renew unexpectedly succeeded")
	}

	if released, err := st.ReleaseAgentDaemonDeviceOwner(ctx, ReleaseAgentDaemonDeviceOwnerInput{
		DeviceID:   deviceID,
		OwnerPodID: "pod-a",
		Generation: first.Generation,
	}); err != nil {
		t.Fatalf("stale release: %v", err)
	} else if released {
		t.Fatal("stale generation release unexpectedly deleted current owner")
	}

	current, ok, err := st.GetAgentDaemonDeviceOwner(ctx, deviceID)
	if err != nil || !ok {
		t.Fatalf("get current owner = %+v %v %v", current, ok, err)
	}
	if current.OwnerPodID != "pod-b" || current.Generation != second.Generation {
		t.Fatalf("current owner changed by stale pod: %+v", current)
	}

	if _, ok, err := st.RenewAgentDaemonDeviceOwner(ctx, RenewAgentDaemonDeviceOwnerInput{
		DeviceID:       deviceID,
		OwnerPodID:     "pod-b",
		Generation:     second.Generation,
		Now:            now.Add(4 * time.Second),
		LeaseExpiresAt: now.Add(4 * time.Minute),
	}); err != nil {
		t.Fatalf("fresh renew: %v", err)
	} else if !ok {
		t.Fatal("fresh generation renew failed")
	}
}
