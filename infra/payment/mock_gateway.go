package payment

import (
	"context"
	"fmt"
	"math/rand"
	"time"
)

// MockGateway simulates a flaky external payment provider: every call has a
// configured chance of failing and a configured latency.
type MockGateway struct {
	failureRate float64
	latency     time.Duration
}

func NewMockGateway(failureRate float64, latency time.Duration) *MockGateway {
	return &MockGateway{failureRate: failureRate, latency: latency}
}

func (g *MockGateway) Charge(ctx context.Context, orderID string, amountCents int64) error {
	select {
	case <-time.After(g.latency):
	case <-ctx.Done():
		return ctx.Err()
	}

	if rand.Float64() < g.failureRate {
		return fmt.Errorf("payment gateway declined charge for order %s", orderID)
	}
	return nil
}
