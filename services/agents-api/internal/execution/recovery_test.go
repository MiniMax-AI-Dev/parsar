package execution

import (
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestExistingSessionRecoveryRequiresVerifiedCapability(t *testing.T) {
	for _, engine := range []string{"codex", "claude_sdk", "future-engine"} {
		for _, started := range []bool{false, true} {
			for _, nativeID := range []string{"", "native"} {
				for _, capable := range []bool{false, true} {
					wantRecovery := started && nativeID == ""
					req, err := (&Dispatcher{}).executionRequest(t.Context(), store.Session{ID: "session", Engine: engine}, Snapshot{}, device.KindCapabilities{NativeSessionRecovery: capable}, store.SessionExecutionBinding{HasStartedTurn: started, NativeSessionID: nativeID})
					if wantRecovery && !capable {
						if err == nil {
							t.Fatal("unverified recovery admitted", engine)
						}
						continue
					}
					if err != nil || req.RequireExistingNativeSession != wantRecovery || req.AgentSessionID != nativeID || !req.StrictResume || req.AgentStateKey != "agents-api-session" {
						t.Fatalf("engine=%s started=%v id=%s capability=%v request=%+v err=%v", engine, started, nativeID, capable, req, err)
					}
				}
			}
		}
	}
}
