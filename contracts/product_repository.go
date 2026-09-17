package contracts

import (
	"context"

	"orderflow/domains/product"
)

type ProductRepository interface {
	Create(ctx context.Context, p *product.Product) error
	GetByID(ctx context.Context, id string) (*product.Product, error)
	List(ctx context.Context, limit, offset int) ([]product.Product, error)
}
