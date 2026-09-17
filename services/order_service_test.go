package services_test

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/mock/gomock"

	"orderflow/contracts/mocks"
	"orderflow/domains/order"
	"orderflow/services"
)

func TestOrderService_GetOrder(t *testing.T) {
	ctx := context.Background()
	someOrder := &order.Order{ID: "order-1", UserID: "user-1"}

	tests := []struct {
		name        string
		requesterID string
		isAdmin     bool
		repoOrder   *order.Order
		repoErr     error
		wantErr     error
	}{
		{
			name:        "owner can view their own order",
			requesterID: "user-1",
			repoOrder:   someOrder,
		},
		{
			name:        "admin can view any order",
			requesterID: "someone-else",
			isAdmin:     true,
			repoOrder:   someOrder,
		},
		{
			name:        "non-owner non-admin is forbidden",
			requesterID: "someone-else",
			repoOrder:   someOrder,
			wantErr:     order.ErrForbidden,
		},
		{
			name:        "repository error passes through unchanged",
			requesterID: "user-1",
			repoErr:     order.ErrNotFound,
			wantErr:     order.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := mocks.NewMockOrderRepository(ctrl)
			repo.EXPECT().GetByID(ctx, "order-1").Return(tt.repoOrder, tt.repoErr)

			svc := services.NewOrderService(repo)
			got, err := svc.GetOrder(ctx, "order-1", tt.requesterID, tt.isAdmin)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected error %v, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.repoOrder {
				t.Fatalf("expected the repository's order to be returned unchanged")
			}
		})
	}
}

func TestOrderService_CreateOrder(t *testing.T) {
	ctx := context.Background()
	items := []order.ItemInput{{ProductID: "p1", Quantity: 2}}

	tests := []struct {
		name    string
		repoErr error
		wantErr error
	}{
		{name: "success"},
		{name: "insufficient stock passes through", repoErr: order.ErrInsufficientStock, wantErr: order.ErrInsufficientStock},
		{name: "product not found passes through", repoErr: order.ErrProductNotFound, wantErr: order.ErrProductNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := mocks.NewMockOrderRepository(ctrl)
			repo.EXPECT().CreateWithItems(ctx, gomock.Any(), items).Return(tt.repoErr)

			svc := services.NewOrderService(repo)
			got, err := svc.CreateOrder(ctx, "user-1", items)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected error %v, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.UserID != "user-1" {
				t.Fatalf("expected the order to carry the requesting user id")
			}
		})
	}
}

func TestOrderService_UpdateStatus(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		repoErr error
		wantErr bool
	}{
		{name: "success"},
		{name: "repository error is wrapped and reported", repoErr: errors.New("db down"), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := mocks.NewMockOrderRepository(ctrl)
			repo.EXPECT().UpdateStatus(ctx, "order-1", "paid").Return(tt.repoErr)

			svc := services.NewOrderService(repo)
			err := svc.UpdateStatus(ctx, "order-1", "paid")

			if tt.wantErr && err == nil {
				t.Fatal("expected an error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
