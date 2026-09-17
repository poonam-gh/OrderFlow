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

func (s *OrderService) CreateOrder(ctx context.Context, userID string, items []order.ItemInput) (*order.Order, error) {
	o := &order.Order{UserID: userID}
	if err := s.repo.CreateWithItems(ctx, o, items); err != nil {
		return nil, err
	}
	return o, nil
}

// GetOrder returns the order only if it belongs to requesterID, unless
// isAdmin is true — an admin can look up any order.
func (s *OrderService) GetOrder(ctx context.Context, id, requesterID string, isAdmin bool) (*order.Order, error) {
	o, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if !isAdmin && o.UserID != requesterID {
		return nil, order.ErrForbidden
	}
	return o, nil
}

func (s *OrderService) ListMyOrders(ctx context.Context, userID string, limit, offset int) ([]order.Order, error) {
	return s.repo.ListByUser(ctx, userID, limit, offset)
}

func (s *OrderService) UpdateStatus(ctx context.Context, id string, status string) error {
	if err := s.repo.UpdateStatus(ctx, id, status); err != nil {
		return fmt.Errorf("update order status: %w", err)
	}
	return nil
}
