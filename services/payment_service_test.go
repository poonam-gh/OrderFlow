package services_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"orderflow/contracts/mocks"
	"orderflow/pkg/circuitbreaker"
	"orderflow/services"
)

func TestPaymentService_ProcessPayment(t *testing.T) {
	ctx := context.Background()
	gatewayErr := errors.New("gateway declined")

	tests := []struct {
		name              string
		gatewayErr        error
		wantPaymentStatus string
		wantOrderStatus   string
		wantErr           error
	}{
		{
			name:              "successful charge marks order and payment paid",
			wantPaymentStatus: "paid",
			wantOrderStatus:   "paid",
		},
		{
			name:              "failed charge marks order and payment failed, error passed through",
			gatewayErr:        gatewayErr,
			wantPaymentStatus: "failed",
			wantOrderStatus:   "payment_failed",
			wantErr:           gatewayErr,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			gateway := mocks.NewMockPaymentGateway(ctrl)
			payments := mocks.NewMockPaymentRepository(ctrl)
			orders := mocks.NewMockOrderRepository(ctrl)

			gateway.EXPECT().Charge(ctx, "order-1", int64(1000)).Return(tt.gatewayErr)
			payments.EXPECT().Create(ctx, "order-1", int64(1000), tt.wantPaymentStatus).Return(nil)
			orders.EXPECT().UpdateStatus(ctx, "order-1", tt.wantOrderStatus).Return(nil)

			breaker := circuitbreaker.New(5, time.Second)
			svc := services.NewPaymentService(gateway, breaker, payments, orders)

			err := svc.ProcessPayment(ctx, "order-1", 1000)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected error %v, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// This is the integration point that matters most: once the breaker trips,
// a second ProcessPayment call must NOT reach the gateway at all — proven
// here by Charge's mock expectation being satisfied exactly once even
// though ProcessPayment is called twice.
func TestPaymentService_ProcessPayment_OpenBreakerSkipsGateway(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	gateway := mocks.NewMockPaymentGateway(ctrl)
	payments := mocks.NewMockPaymentRepository(ctrl)
	orders := mocks.NewMockOrderRepository(ctrl)

	gateway.EXPECT().Charge(ctx, "order-1", int64(1000)).Return(errors.New("boom")).Times(1)
	payments.EXPECT().Create(ctx, "order-1", int64(1000), "failed").Return(nil).Times(2)
	orders.EXPECT().UpdateStatus(ctx, "order-1", "payment_failed").Return(nil).Times(2)

	breaker := circuitbreaker.New(1, time.Minute) // trips after just 1 failure
	svc := services.NewPaymentService(gateway, breaker, payments, orders)

	_ = svc.ProcessPayment(ctx, "order-1", 1000) // fails, trips the breaker

	err := svc.ProcessPayment(ctx, "order-1", 1000) // breaker now open
	if !errors.Is(err, circuitbreaker.ErrOpen) {
		t.Fatalf("expected ErrOpen once the breaker is tripped, got %v", err)
	}
}
