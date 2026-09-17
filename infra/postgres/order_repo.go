package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"

	"orderflow/domains/order"
	"orderflow/pkg/requestid"
)

type OrderRepository struct {
	db *sqlx.DB
}

func NewOrderRepository(db *sqlx.DB) *OrderRepository {
	return &OrderRepository{db: db}
}

type outboxOrderCreatedPayload struct {
	OrderID    string `json:"order_id"`
	TotalCents int64  `json:"total_cents"`
	RequestID  string `json:"request_id,omitempty"`
}

// CreateWithItems reserves stock for every item, inserts the order + items,
// and writes the "order.created" outbox event, all in one transaction —
// the outbox pattern: either everything here commits together, or none of
// it does, so an outbox event is never lost or created without its order.
func (r *OrderRepository) CreateWithItems(ctx context.Context, o *order.Order, items []order.ItemInput) error {
	if len(items) == 0 {
		return order.ErrEmptyItems
	}

	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	var total int64
	resolved := make([]order.Item, 0, len(items))

	for _, item := range items {
		var priceCents int64
		var stock int
		err := tx.QueryRowxContext(ctx,
			`SELECT price_cents, stock FROM products WHERE id = $1 FOR UPDATE`, item.ProductID,
		).Scan(&priceCents, &stock)
		if errors.Is(err, sql.ErrNoRows) {
			return order.ErrProductNotFound
		}
		if err != nil {
			return fmt.Errorf("lock product %s: %w", item.ProductID, err)
		}
		if stock < item.Quantity {
			return order.ErrInsufficientStock
		}

		if _, err := tx.ExecContext(ctx,
			`UPDATE products SET stock = stock - $1, updated_at = now() WHERE id = $2`,
			item.Quantity, item.ProductID,
		); err != nil {
			return fmt.Errorf("decrement stock for %s: %w", item.ProductID, err)
		}

		total += priceCents * int64(item.Quantity)
		resolved = append(resolved, order.Item{
			ProductID:      item.ProductID,
			Quantity:       item.Quantity,
			UnitPriceCents: priceCents,
		})
	}

	o.TotalCents = total
	err = tx.QueryRowxContext(ctx,
		`INSERT INTO orders (user_id, total_cents) VALUES ($1, $2)
         RETURNING id, status, created_at, updated_at`,
		o.UserID, o.TotalCents,
	).Scan(&o.ID, &o.Status, &o.CreatedAt, &o.UpdatedAt)
	if err != nil {
		return fmt.Errorf("insert order: %w", err)
	}

	for i := range resolved {
		resolved[i].OrderID = o.ID
		if err := tx.QueryRowxContext(ctx,
			`INSERT INTO order_items (order_id, product_id, quantity, unit_price_cents)
             VALUES ($1, $2, $3, $4) RETURNING id`,
			resolved[i].OrderID, resolved[i].ProductID, resolved[i].Quantity, resolved[i].UnitPriceCents,
		).Scan(&resolved[i].ID); err != nil {
			return fmt.Errorf("insert order item: %w", err)
		}
	}
	o.Items = resolved

	reqID, _ := requestid.FromContext(ctx)
	payload, err := json.Marshal(outboxOrderCreatedPayload{OrderID: o.ID, TotalCents: o.TotalCents, RequestID: reqID})
	if err != nil {
		return fmt.Errorf("marshal outbox payload: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO outbox_events (event_type, payload) VALUES ($1, $2)`,
		"order.created", payload,
	); err != nil {
		return fmt.Errorf("insert outbox event: %w", err)
	}

	return tx.Commit()
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

	if err := r.db.SelectContext(ctx, &o.Items, `SELECT * FROM order_items WHERE order_id = $1`, id); err != nil {
		return nil, fmt.Errorf("get order items: %w", err)
	}

	return &o, nil
}

func (r *OrderRepository) ListByUser(ctx context.Context, userID string, limit, offset int) ([]order.Order, error) {
	var orders []order.Order
	err := r.db.SelectContext(ctx, &orders,
		`SELECT * FROM orders WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
		userID, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list orders: %w", err)
	}
	return orders, nil
}

func (r *OrderRepository) UpdateStatus(ctx context.Context, id string, status string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE orders SET status = $1, updated_at = now() WHERE id = $2`, status, id)
	if err != nil {
		return fmt.Errorf("update order status: %w", err)
	}
	return nil
}
