package services_test

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/mock/gomock"

	"orderflow/contracts/mocks"
	"orderflow/domains/product"
	"orderflow/services"
)

func TestProductService_GetProduct(t *testing.T) {
	ctx := context.Background()
	p := &product.Product{ID: "prod-1", Name: "Widget"}

	tests := []struct {
		name     string
		cacheHit bool
	}{
		{name: "cache hit skips the repository entirely", cacheHit: true},
		{name: "cache miss falls back to the repository and populates the cache", cacheHit: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := mocks.NewMockProductRepository(ctrl)
			cache := mocks.NewMockProductCache(ctrl)

			if tt.cacheHit {
				cache.EXPECT().Get(ctx, "prod-1").Return(p, true)
				// No repo.EXPECT() set: an unexpected call to the repository
				// here would fail the test, proving the cache hit short-circuits it.
			} else {
				cache.EXPECT().Get(ctx, "prod-1").Return(nil, false)
				repo.EXPECT().GetByID(ctx, "prod-1").Return(p, nil)
				cache.EXPECT().Set(ctx, p)
			}

			svc := services.NewProductService(repo, cache)
			got, err := svc.GetProduct(ctx, "prod-1")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != p {
				t.Fatalf("expected the product to be returned")
			}
		})
	}
}

func TestProductService_GetProduct_RepositoryError(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	repo := mocks.NewMockProductRepository(ctrl)
	cache := mocks.NewMockProductCache(ctrl)

	cache.EXPECT().Get(ctx, "missing").Return(nil, false)
	repo.EXPECT().GetByID(ctx, "missing").Return(nil, product.ErrNotFound)

	svc := services.NewProductService(repo, cache)
	_, err := svc.GetProduct(ctx, "missing")
	if !errors.Is(err, product.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
