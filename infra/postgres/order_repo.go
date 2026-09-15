package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

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

func (r *OrderRepository) GetByID(ctx context.Context, id string) (*order.Order, error) {
	var o order.Order
	err := r.db.GetContext(ctx, &o, `SELECT * FROM orders WHERE id = $1`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, order.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get order: %w", err)
	}
	return &o, nil
}
