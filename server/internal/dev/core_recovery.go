package dev

import (
	"context"
	"sync"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type coreRecoveryStore interface {
	RuntimeStore
	GetCoreExecutionStatus(context.Context, string) (string, error)
	ListRecoverableCoreRuns(context.Context) ([]store.StreamingDispatchInput, error)
}

func RecoverCoreRuns(ctx context.Context, st coreRecoveryStore, deps StreamingDispatchDeps) {
	var active sync.Map
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()
	for {
		runs, err := st.ListRecoverableCoreRuns(ctx)
		if err != nil && ctx.Err() == nil {
			log.Error(ctx, "Core recovery scan failed")
		}
		for _, run := range runs {
			if _, loaded := active.LoadOrStore(run.RunID, true); loaded {
				continue
			}
			go func(run store.StreamingDispatchInput) {
				defer active.Delete(run.RunID)
				current, err := st.GetCoreExecutionStatus(ctx, run.RunID)
				if err != nil {
					return
				}
				if current == "queued" {
					_, _ = StartConversationRun(ctx, st, deps, run.RunID, run.ConversationID)
					return
				}
				if current != "queued" {
					dispatchConversationRun(ctx, st, deps.routerConfig(), run.RunID)
				}
			}(run)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
