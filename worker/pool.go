package worker

import (
	"context"
	"log/slog"
)

// Pool runs N goroutines dispatching Jobs to a Handler. Submit is safe for concurrent use.
type Pool struct {
	workers int
	handler Handler
	jobs    chan Job
}

func NewPool(workers int, handler Handler) *Pool {
	return &Pool{
		workers: workers,
		handler: handler,
		jobs:    make(chan Job, workers*2),
	}
}

func (p *Pool) Submit(job Job) {
	p.jobs <- job
}

func (p *Pool) Run(ctx context.Context) error {
	done := make(chan struct{}, p.workers)
	for i := 0; i < p.workers; i++ {
		go p.worker(ctx, i, done)
	}

	<-ctx.Done()
	for i := 0; i < p.workers; i++ {
		<-done
	}
	return nil
}

func (p *Pool) worker(ctx context.Context, id int, done chan<- struct{}) {
	defer func() { done <- struct{}{} }()

	for {
		select {
		case <-ctx.Done():
			return
		case job := <-p.jobs:
			if err := p.handler.Handle(ctx, job); err != nil {
				slog.Error("job failed", "worker", id, "job_id", job.ID, "type", job.Type, "err", err)
			}
		}
	}
}
