package contracts

import (
	"context"

	"orderflow/domains/order"
)

type OrderRepository interface {
	Create(ctx context.Context, o *order.Order) error
}
