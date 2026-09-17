package contracts

import "context"

// PaymentGateway represents an external payment provider. Calls to it go
// through a circuit breaker (pkg/circuitbreaker) since it's the one
// dependency in this system modeling an unreliable external service.
type PaymentGateway interface {
	Charge(ctx context.Context, orderID string, amountCents int64) error
}

type PaymentRepository interface {
	Create(ctx context.Context, orderID string, amountCents int64, status string) error
}
