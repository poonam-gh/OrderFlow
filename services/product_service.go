package services

import (
	"context"
	"fmt"

	"orderflow/contracts"
	"orderflow/domains/product"
)

type ProductService struct {
	repo  contracts.ProductRepository
	cache contracts.ProductCache
}

func NewProductService(repo contracts.ProductRepository, cache contracts.ProductCache) *ProductService {
	return &ProductService{repo: repo, cache: cache}
}

func (s *ProductService) CreateProduct(ctx context.Context, name string, priceCents int64, stock int) (*product.Product, error) {
	p := &product.Product{Name: name, PriceCents: priceCents, Stock: stock}
	if err := s.repo.Create(ctx, p); err != nil {
		return nil, fmt.Errorf("create product: %w", err)
	}
	return p, nil
}

// GetProduct is cache-aside: check Redis first, fall back to Postgres on a
// miss, then populate the cache for next time.
func (s *ProductService) GetProduct(ctx context.Context, id string) (*product.Product, error) {
	if p, ok := s.cache.Get(ctx, id); ok {
		return p, nil
	}

	p, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	s.cache.Set(ctx, p)
	return p, nil
}

func (s *ProductService) ListProducts(ctx context.Context, limit, offset int) ([]product.Product, error) {
	return s.repo.List(ctx, limit, offset)
}
