package execution

import (
	"context"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
)

// provisionPending shares the existing lifecycle owner and serial gate. This
// also recovers idle Session creation interrupted after its database commit.
func (r *runtimeLifecycle) provisionPending(ctx context.Context) error {
	if r.config.DefaultProvider == "" {
		return nil
	}
	rows, err := r.store.ListUnallocatedHostedEnvironments(ctx, r.pendingCursor)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		r.pendingCursor = ""
		return nil
	}
	for _, environment := range rows {
		r.pendingCursor = environment.ID
		operation, cancel := context.WithTimeout(ctx, 30*time.Second)
		_, err := r.provision(operation, environment.TenantID, environment.ID, r.config.DefaultProvider)
		cancel()
		if err != nil {
			if ownership := r.store.CheckExecutionOwnership(ctx); ownership != nil {
				return ownership
			}
			log.Ctx(ctx).Warn("managed Runtime bootstrap incomplete", "environment_id", environment.ID)
		}
	}
	return nil
}
