package localworkspace

import (
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"testing"
)

func TestRuntimeNetworkPolicyMustMatchExecutionButNotReadOnly(t *testing.T) {
	for _, deployed := range []string{"", "enabled", "disabled"} {
		for _, requested := range []string{"", "enabled", "disabled", "restricted"} {
			b, req := testBinding(t)
			b.networkAccess = deployed
			req.LocalEnvironment.NetworkAccess = requested
			_, err := b.Configure(req)
			if (err == nil) != (deployed == requested) {
				t.Fatalf("execution policy %q/%q: %v", deployed, requested, err)
			}
			req.WorkspaceReadOnly = true
			req.LocalEnvironment = &proto.LocalEnvironment{ID: b.environment}
			if _, err := b.Configure(req); err != nil {
				t.Fatal("read-only operation requires unrelated execution policy", err)
			}
		}
	}
}
