package worker

import "context"

// Job is a unit of work pulled off a queue (e.g. a row from outbox_events).
type Job struct {
	ID      string
	Type    string // e.g. "order.created"
	Payload []byte // raw JSON payload
}

// Handler processes a single Job. services.Delegator is the production implementation.
type Handler interface {
	Handle(ctx context.Context, job Job) error
}
