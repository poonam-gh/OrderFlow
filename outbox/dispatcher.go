// Package outbox polls outbox_events for pending rows and feeds them into
// a worker.Pool as Jobs. It depends only on contracts.OutboxRepository and
// worker.Pool — it has no idea what an order or a payment is.
package outbox

import (
	"context"
	"log/slog"
	"time"

	"orderflow/contracts"
	"orderflow/worker"
)

type Dispatcher struct {
	repo      contracts.OutboxRepository
	pool      *worker.Pool
	interval  time.Duration
	batchSize int
	logger    *slog.Logger
}

func NewDispatcher(repo contracts.OutboxRepository, pool *worker.Pool, interval time.Duration, batchSize int, logger *slog.Logger) *Dispatcher {
	return &Dispatcher{repo: repo, pool: pool, interval: interval, batchSize: batchSize, logger: logger}
}

func (d *Dispatcher) Run(ctx context.Context) error {
	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			d.pollAndDispatch(ctx)
		}
	}
}

func (d *Dispatcher) pollAndDispatch(ctx context.Context) {
	events, err := d.repo.Claim(ctx, d.batchSize)
	if err != nil {
		d.logger.Error("claim outbox events failed", "err", err)
		return
	}

	for _, e := range events {
		d.pool.Submit(worker.Job{ID: e.ID, Type: e.EventType, Payload: e.Payload})
	}
}
