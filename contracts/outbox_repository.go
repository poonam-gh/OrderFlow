//go:generate mockgen -source=outbox_repository.go -destination=mocks/mock_outbox_repository.go -package=mocks
package contracts

import "context"

type OutboxEvent struct {
	ID        string
	EventType string
	Payload   []byte
}

// OutboxRepository claims pending outbox rows and marks them processed/failed.
// Claim uses SELECT ... FOR UPDATE SKIP LOCKED under the hood, so multiple
// dispatcher instances can poll the same table without processing the same
// row twice or blocking each other.
type OutboxRepository interface {
	Claim(ctx context.Context, limit int) ([]OutboxEvent, error)
	MarkProcessed(ctx context.Context, id string) error
	MarkFailed(ctx context.Context, id string) error
}
