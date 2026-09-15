package postgres

import (
	"context"

	"github.com/jmoiron/sqlx"

	"orderflow/domains/order"
)

type OrderRepository struct {
	db *sqlx.DB
}

func NewOrderRepository(db *sqlx.DB) *OrderRepository {
	return &OrderRepository{db: db}
}

func (r *OrderRepository) Create(ctx context.Context, o *order.Order) error {
	query := `INSERT INTO orders (user_id, total_cents) VALUES ($1, $2)
              RETURNING id, status, created_at, updated_at`
	return r.db.QueryRowxContext(ctx, query, o.UserID, o.TotalCents).
		Scan(&o.ID, &o.Status, &o.CreatedAt, &o.UpdatedAt)
}
