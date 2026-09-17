package postgres

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"

	"orderflow/contracts"
)

type OutboxRepository struct {
	db *sqlx.DB
}

func NewOutboxRepository(db *sqlx.DB) *OutboxRepository {
	return &OutboxRepository{db: db}
}

// Claim atomically grabs up to limit pending rows and flips them to
// "processing" in one statement, so a batch is claimed without holding a
// transaction open for the (potentially slow) processing that follows.
// FOR UPDATE SKIP LOCKED means multiple dispatcher instances could poll
// this same table concurrently without double-claiming a row.
func (r *OutboxRepository) Claim(ctx context.Context, limit int) ([]contracts.OutboxEvent, error) {
	query := `
        WITH claimed AS (
            SELECT id FROM outbox_events
            WHERE status = 'pending'
            ORDER BY created_at
            LIMIT $1
            FOR UPDATE SKIP LOCKED
        )
        UPDATE outbox_events
        SET status = 'processing'
        WHERE id IN (SELECT id FROM claimed)
        RETURNING id, event_type, payload`

	rows, err := r.db.QueryxContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("claim outbox events: %w", err)
	}
	defer rows.Close()

	var events []contracts.OutboxEvent
	for rows.Next() {
		var e contracts.OutboxEvent
		if err := rows.Scan(&e.ID, &e.EventType, &e.Payload); err != nil {
			return nil, fmt.Errorf("scan outbox event: %w", err)
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

func (r *OutboxRepository) MarkProcessed(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE outbox_events SET status = 'processed', processed_at = now() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("mark outbox event processed: %w", err)
	}
	return nil
}

func (r *OutboxRepository) MarkFailed(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE outbox_events SET status = 'failed' WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("mark outbox event failed: %w", err)
	}
	return nil
}
