package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"

	"orderflow/domains/product"
)

type ProductRepository struct {
	db *sqlx.DB
}

func NewProductRepository(db *sqlx.DB) *ProductRepository {
	return &ProductRepository{db: db}
}

func (r *ProductRepository) Create(ctx context.Context, p *product.Product) error {
	query := `INSERT INTO products (name, price_cents, stock) VALUES ($1, $2, $3)
              RETURNING id, created_at, updated_at`
	return r.db.QueryRowxContext(ctx, query, p.Name, p.PriceCents, p.Stock).
		Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt)
}

func (r *ProductRepository) GetByID(ctx context.Context, id string) (*product.Product, error) {
	var p product.Product
	err := r.db.GetContext(ctx, &p, `SELECT * FROM products WHERE id = $1`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, product.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get product: %w", err)
	}
	return &p, nil
}

func (r *ProductRepository) List(ctx context.Context, limit, offset int) ([]product.Product, error) {
	var products []product.Product
	err := r.db.SelectContext(ctx, &products,
		`SELECT * FROM products ORDER BY created_at DESC LIMIT $1 OFFSET $2`, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list products: %w", err)
	}
	return products, nil
}
