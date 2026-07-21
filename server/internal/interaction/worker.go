package interaction

import (
	"context"
	"time"
)

type WorkerOptions struct {
	Interval   time.Duration
	ClaimLease time.Duration
	BatchSize  int32
}

type Worker struct {
	service    *Service
	store      Store
	interval   time.Duration
	claimLease time.Duration
	batchSize  int32
}

func NewWorker(service *Service, store Store, options WorkerOptions) *Worker {
	interval := options.Interval
	if interval <= 0 {
		interval = 15 * time.Second
	}
	claimLease := options.ClaimLease
	if claimLease <= 0 {
		claimLease = time.Minute
	}
	batchSize := options.BatchSize
	if batchSize <= 0 {
		batchSize = 100
	}
	return &Worker{service: service, store: store, interval: interval, claimLease: claimLease, batchSize: batchSize}
}

func (w *Worker) Run(ctx context.Context) error {
	if err := w.Sweep(ctx); err != nil {
		w.service.warn("initial interaction expiry sweep failed", "error", err)
	}
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := w.Sweep(ctx); err != nil {
				w.service.warn("interaction expiry sweep failed", "error", err)
			}
		}
	}
}

func (w *Worker) Sweep(ctx context.Context) error {
	now := w.service.now()
	if _, err := w.store.ReleaseStaleAgentInteractionClaims(ctx, now.Add(-w.claimLease), now); err != nil {
		return err
	}
	ids, err := w.store.ListExpiredPendingAgentInteractionIDs(ctx, now, w.batchSize)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := w.service.Expire(ctx, id); err != nil {
			w.service.warn("expire interaction failed", "interaction_id", id, "error", err)
		}
	}
	return nil
}
