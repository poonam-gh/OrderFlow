package order

import (
	"errors"
	"time"
)

var ErrNotFound = errors.New("order not found")

type Order struct {
	ID         string    `db:"id" json:"id"`
	UserID     string    `db:"user_id" json:"user_id"`
	Status     string    `db:"status" json:"status"`
	TotalCents int64     `db:"total_cents" json:"total_cents"`
	CreatedAt  time.Time `db:"created_at" json:"created_at"`
	UpdatedAt  time.Time `db:"updated_at" json:"updated_at"`
}
