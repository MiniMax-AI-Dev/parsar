package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

type fakeOwnerStore struct {
	renewOK      bool
	renewCalls   int
	releaseCalls int
	releaseCh    chan struct{}
	lastRenewGen int64
}

func (f *fakeOwnerStore) ClaimAgentDaemonDeviceOwner(context.Context, device.ClaimOwner) (device.Owner, error) {
	return device.Owner{}, nil
}

func (f *fakeOwnerStore) RenewAgentDaemonDeviceOwner(_ context.Context, in device.RenewOwner) (device.Owner, bool, error) {
	f.renewCalls++
	f.lastRenewGen = in.Generation
	return device.Owner{}, f.renewOK, nil
}

func (f *fakeOwnerStore) ReleaseAgentDaemonDeviceOwner(context.Context, device.ReleaseOwner) (bool, error) {
	f.releaseCalls++
	if f.releaseCh != nil {
		select {
		case <-f.releaseCh:
		default:
			close(f.releaseCh)
		}
	}
	return true, nil
}

func (f *fakeOwnerStore) GetAgentDaemonDeviceOwner(context.Context, string) (device.Owner, bool, error) {
	return device.Owner{}, false, nil
}

func TestSessionOwnerLeaseLostClosesStaleConnection(t *testing.T) {
	reg := NewRegistry()
	conn := newFakeConn()
	owners := &fakeOwnerStore{renewOK: false, releaseCh: make(chan struct{})}
	lease := &ownerLease{store: owners, deviceID: "dev-1", ownerPodID: "pod-a", generation: 7, ttl: time.Minute}
	sess := NewSessionWithOwner(conn, "dev-1", "wks-1", proto.Version, reg, nil, lease)
	reg.Register(sess)
	sess.Start()

	heartbeat, _ := proto.NewEnvelope(proto.TypeHeartbeat, "", proto.HeartbeatPayload{})
	raw, _ := jsonMarshal(heartbeat)
	conn.Feed(raw)

	select {
	case <-sess.Closed():
	case <-time.After(2 * time.Second):
		t.Fatal("session did not close after owner renew returned false")
	}
	if owners.renewCalls == 0 || owners.lastRenewGen != 7 {
		t.Fatalf("renew not called with generation 7: calls=%d gen=%d", owners.renewCalls, owners.lastRenewGen)
	}
	select {
	case <-owners.releaseCh:
	case <-time.After(2 * time.Second):
		t.Fatal("release not called on close")
	}
	if owners.releaseCalls == 0 {
		t.Fatal("release count not incremented")
	}
}
