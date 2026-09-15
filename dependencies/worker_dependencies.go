package dependencies

import (
	"context"

	"orderflow/app"
	"orderflow/services"
	"orderflow/worker"
)

const defaultWorkerCount = 4

// WorkerDependencies holds what the worker entrypoint needs: the delegator and the pool.
type WorkerDependencies struct {
	App       *app.AppContext
	Delegator *services.Delegator
	Pool      *worker.Pool
	workers   int
}

func NewWorkerDependencies(appCtx *app.AppContext) *WorkerDependencies {
	delegator := services.NewDelegator()

	count := appCtx.Config.Worker.Count
	if count <= 0 {
		count = defaultWorkerCount
	}

	pool := worker.NewPool(count, delegator)

	return &WorkerDependencies{
		App:       appCtx,
		Delegator: delegator,
		Pool:      pool,
		workers:   count,
	}
}

// Run starts the worker pool and blocks until ctx is cancelled.
func (w *WorkerDependencies) Run(ctx context.Context) error {
	w.App.Logger.Info("worker pool starting", "workers", w.workers)
	return w.Pool.Run(ctx)
}
