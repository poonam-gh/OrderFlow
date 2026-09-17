package product

import (
	"errors"
	"time"
)

var (
	ErrNotFound          = errors.New("product not found")
	ErrInsufficientStock = errors.New("insufficient stock")
)

type Product struct {
	ID         string    `db:"id" json:"id"`
	Name       string    `db:"name" json:"name"`
	PriceCents int64     `db:"price_cents" json:"price_cents"`
	Stock      int       `db:"stock" json:"stock"`
	CreatedAt  time.Time `db:"created_at" json:"created_at"`
	UpdatedAt  time.Time `db:"updated_at" json:"updated_at"`
}
