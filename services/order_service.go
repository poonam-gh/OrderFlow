package services

import (
	"context"
	"fmt"

	"orderflow/contracts"
	"orderflow/domains/order"
)

type OrderService struct {
	repo contracts.OrderRepository
}

func NewOrderService(repo contracts.OrderRepository) *OrderService {
	return &OrderService{repo: repo}
}

func (s *OrderService) CreateOrder(ctx context.Context, userID string, totalCents int64) (*order.Order, error) {
	o := &order.Order{UserID: userID, TotalCents: totalCents}
	if err := s.repo.Create(ctx, o); err != nil {
		return nil, fmt.Errorf("create order: %w", err)
	}
	return o, nil
}
