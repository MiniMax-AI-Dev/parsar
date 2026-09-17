package execution

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestDirectoryPreparationBoundsConnectionResolution(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		d := &Dispatcher{EnvironmentConnection: func(ctx context.Context, _ store.Session, _ store.Environment) (EnvironmentConnection, error) {
			<-ctx.Done()
			return EnvironmentConnection{}, ctx.Err()
		}}
		started := time.Now()
		result := d.readPreparedDirectory(context.Background(), nil, store.Session{}, store.Environment{}, "/workspace", proto.WorkspaceReadPayload{})
		if !errors.Is(result.err, ErrExecutionUnavailable) || time.Since(started) <= 0 || time.Since(started) > 45*time.Second {
			t.Fatal("connection resolution did not have a bounded owner lifetime")
		}
	})
}
