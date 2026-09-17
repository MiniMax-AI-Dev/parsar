package execution

import (
	"context"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestLocalEnvironmentRequiresQualifiedProfileAndExactAuthority(t *testing.T) {
	for _, configuration := range []string{
		`{"type":"openai_hosted"}`,
		`{"type":"openai_hosted","network":null}`,
		`{"type":"openai_hosted","network":{"access":"enabled"}}`,
		`{"type":"openai_hosted","network":{"access":"disabled","allow":["example.com"]}}`,
		`{"type":"openai_hosted","network":{"access":"disabled"},"workspace_directory":"/override"}`,
	} {
		if _, err := parseEnvironmentPlacement([]byte(configuration)); err == nil {
			t.Fatalf("unqualified private profile accepted: %s", configuration)
		}
	}
	session := store.Session{ID: "session", TenantID: "tenant"}
	environment := store.Environment{ID: "environment", SessionID: session.ID, TenantID: session.TenantID, Configuration: []byte(`{"type":"openai_hosted","network":{"access":"disabled"}}`)}
	d := &Dispatcher{EnvironmentConnection: func(context.Context, store.Session, store.Environment) (EnvironmentConnection, error) {
		t.Fatal("local placement resolved a remote transport")
		return EnvironmentConnection{}, nil
	}}
	for _, scope := range []string{"", "other", environment.ID} {
		var req proto.PromptRequestPayload
		release, err := d.configurePreparedEnvironment(t.Context(), session, environment, store.ExecutionDevice{EnvironmentID: scope}, &req)
		if scope == environment.ID {
			if err != nil || release != nil || req.LocalEnvironment == nil || req.LocalEnvironment.ID != environment.ID || req.RemoteEnvironment != nil || req.WorkDir != "" {
				t.Fatal("local identity was not preserved", err)
			}
		} else if err == nil || req.LocalEnvironment != nil {
			t.Fatal("unscoped or foreign authority accepted")
		}
	}
}
