package postgres

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
)

type PaymentRepository struct {
	db *sqlx.DB
}

func NewPaymentRepository(db *sqlx.DB) *PaymentRepository {
	return &PaymentRepository{db: db}
}

func (r *PaymentRepository) Create(ctx context.Context, orderID string, amountCents int64, status string) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO payments (order_id, amount_cents, status) VALUES ($1, $2, $3)`,
		orderID, amountCents, status,
	)
	if err != nil {
		return fmt.Errorf("create payment: %w", err)
	}
	return nil
}
