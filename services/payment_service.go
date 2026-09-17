package services

import (
	"context"
	"fmt"

	"orderflow/contracts"
	"orderflow/pkg/circuitbreaker"
)

type PaymentService struct {
	gateway  contracts.PaymentGateway
	breaker  *circuitbreaker.Breaker
	payments contracts.PaymentRepository
	orders   contracts.OrderRepository
}

func NewPaymentService(
	gateway contracts.PaymentGateway,
	breaker *circuitbreaker.Breaker,
	payments contracts.PaymentRepository,
	orders contracts.OrderRepository,
) *PaymentService {
	return &PaymentService{gateway: gateway, breaker: breaker, payments: payments, orders: orders}
}

// ProcessPayment charges through the circuit breaker, records the payment
// attempt, and updates the order status either way. The gateway/breaker
// error (including circuitbreaker.ErrOpen) is returned to the caller for
// logging/metrics — payment and order state are always persisted first.
func (s *PaymentService) ProcessPayment(ctx context.Context, orderID string, amountCents int64) error {
	chargeErr := s.breaker.Execute(func() error {
		return s.gateway.Charge(ctx, orderID, amountCents)
	})

	paymentStatus, orderStatus := "paid", "paid"
	if chargeErr != nil {
		paymentStatus, orderStatus = "failed", "payment_failed"
	}

	if err := s.payments.Create(ctx, orderID, amountCents, paymentStatus); err != nil {
		return fmt.Errorf("record payment: %w", err)
	}
	if err := s.orders.UpdateStatus(ctx, orderID, orderStatus); err != nil {
		return fmt.Errorf("update order status: %w", err)
	}

	return chargeErr
}

func (s *PaymentService) BreakerState() circuitbreaker.State {
	return s.breaker.State()
}
