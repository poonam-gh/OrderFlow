//go:generate mockgen -source=order_repository.go -destination=mocks/mock_order_repository.go -package=mocks
package contracts

import (
	"context"

	"orderflow/domains/order"
)

type OrderRepository interface {
	CreateWithItems(ctx context.Context, o *order.Order, items []order.ItemInput) error
	GetByID(ctx context.Context, id string) (*order.Order, error)
	ListByUser(ctx context.Context, userID string, limit, offset int) ([]order.Order, error)
	UpdateStatus(ctx context.Context, id string, status string) error
}
