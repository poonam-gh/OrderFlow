package services

import (
	"context"
	"errors"
	"fmt"

	"orderflow/contracts"
	"orderflow/domains/order"
)

// ErrUserNotFound is returned when an order is requested for a user_id that
// doesn't exist in the users table. Handlers map this to a 400, rather than
// letting the request fail as a raw foreign-key violation from Postgres.
var ErrUserNotFound = errors.New("user not found")

type OrderService struct {
	orderRepo contracts.OrderRepository
	userRepo  contracts.UserRepository
}

func NewOrderService(orderRepo contracts.OrderRepository, userRepo contracts.UserRepository) *OrderService {
	return &OrderService{orderRepo: orderRepo, userRepo: userRepo}
}

func (s *OrderService) CreateOrder(ctx context.Context, userID string, totalCents int64) (*order.Order, error) {
	exists, err := s.userRepo.ExistsByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("check user exists: %w", err)
	}
	if !exists {
		return nil, ErrUserNotFound
	}

	o := &order.Order{UserID: userID, TotalCents: totalCents}
	if err := s.orderRepo.Create(ctx, o); err != nil {
		return nil, fmt.Errorf("create order: %w", err)
	}
	return o, nil
}
