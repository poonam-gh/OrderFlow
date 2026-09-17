package order

import (
	"errors"
	"time"
)

var (
	ErrNotFound          = errors.New("order not found")
	ErrForbidden         = errors.New("order does not belong to this user")
	ErrEmptyItems        = errors.New("order must have at least one item")
	ErrProductNotFound   = errors.New("product not found")
	ErrInsufficientStock = errors.New("insufficient stock")
)

type Item struct {
	ID             string `db:"id" json:"id"`
	OrderID        string `db:"order_id" json:"order_id"`
	ProductID      string `db:"product_id" json:"product_id"`
	Quantity       int    `db:"quantity" json:"quantity"`
	UnitPriceCents int64  `db:"unit_price_cents" json:"unit_price_cents"`
}

type ItemInput struct {
	ProductID string
	Quantity  int
}

type Order struct {
	ID         string    `db:"id" json:"id"`
	UserID     string    `db:"user_id" json:"user_id"`
	Status     string    `db:"status" json:"status"`
	TotalCents int64     `db:"total_cents" json:"total_cents"`
	CreatedAt  time.Time `db:"created_at" json:"created_at"`
	UpdatedAt  time.Time `db:"updated_at" json:"updated_at"`
	Items      []Item    `db:"-" json:"items,omitempty"`
}
