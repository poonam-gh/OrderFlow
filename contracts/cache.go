//go:generate mockgen -source=cache.go -destination=mocks/mock_cache.go -package=mocks
package contracts

import (
	"context"

	"orderflow/domains/product"
)

// ProductCache is a cache-aside layer in front of ProductRepository. A miss
// (ok == false) means "go read the repository" — the cache is never the
// source of truth.
type ProductCache interface {
	Get(ctx context.Context, id string) (*product.Product, bool)
	Set(ctx context.Context, p *product.Product)
}
