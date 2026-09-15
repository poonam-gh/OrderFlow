package services

import (
	"context"
	"fmt"

	"orderflow/worker"
)

// EventHandlerFunc handles one worker.Job for a specific event type.
type EventHandlerFunc func(ctx context.Context, job worker.Job) error

// Delegator implements worker.Handler, routing each Job to the function registered for its Type.
type Delegator struct {
	handlers map[string]EventHandlerFunc
}

func NewDelegator() *Delegator {
	return &Delegator{handlers: make(map[string]EventHandlerFunc)}
}

func (d *Delegator) Register(eventType string, handler EventHandlerFunc) {
	d.handlers[eventType] = handler
}

func (d *Delegator) Handle(ctx context.Context, job worker.Job) error {
	handler, ok := d.handlers[job.Type]
	if !ok {
		return fmt.Errorf("no handler registered for event type %q", job.Type)
	}
	return handler(ctx, job)
}
