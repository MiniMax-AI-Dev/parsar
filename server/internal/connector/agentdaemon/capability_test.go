package agentdaemon

import (
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/agentdaemon/gateway"
)

func feedAgentKinds(t *testing.T, conn *fakeConn, sess *gateway.Session, kinds []proto.SupportedAgentKind) {
	t.Helper()
	env, _ := proto.NewEnvelope(proto.TypeHeartbeat, "", proto.HeartbeatPayload{SupportedAgentKinds: kinds})
	conn.Feed(env)
	deadline := time.Now().Add(2 * time.Second)
	for {
		allReady := true
		for _, kind := range kinds {
			if kind.Kind == "" {
				continue
			}
			_, found, snapshotKnown := sess.AgentKindStatus(kind.Kind)
			if !snapshotKnown || !found {
				allReady = false
				break
			}
		}
		if allReady {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("heartbeat kinds were not observed by session: %#v", kinds)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
