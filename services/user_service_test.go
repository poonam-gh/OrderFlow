package services_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/mock/gomock"
	"golang.org/x/crypto/bcrypt"

	"orderflow/contracts/mocks"
	"orderflow/domains/user"
	"orderflow/services"
)

func TestUserService_Register(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		repoErr error
		wantErr error
	}{
		{name: "success"},
		{name: "duplicate email passes through", repoErr: user.ErrEmailTaken, wantErr: user.ErrEmailTaken},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := mocks.NewMockUserRepository(ctrl)
			repo.EXPECT().Create(ctx, gomock.Any()).Return(tt.repoErr)

			svc := services.NewUserService(repo, "secret", time.Hour)
			got, err := svc.Register(ctx, "a@b.com", "password123")

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected error %v, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Email != "a@b.com" {
				t.Fatalf("expected email to be set on the returned user")
			}
			if got.PasswordHash == "" || got.PasswordHash == "password123" {
				t.Fatalf("expected the password to be hashed, not stored/returned as plaintext")
			}
		})
	}
}

func TestUserService_Login(t *testing.T) {
	ctx := context.Background()
	hash, err := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("setup: hash password: %v", err)
	}
	existing := &user.User{ID: "user-1", Email: "a@b.com", PasswordHash: string(hash), Role: "customer"}

	tests := []struct {
		name     string
		password string
		repoUser *user.User
		repoErr  error
		wantErr  bool
	}{
		{name: "correct password succeeds", password: "correct-password", repoUser: existing},
		{name: "wrong password is rejected", password: "wrong-password", repoUser: existing, wantErr: true},
		{name: "unknown email is rejected", password: "correct-password", repoErr: user.ErrNotFound, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo := mocks.NewMockUserRepository(ctrl)
			repo.EXPECT().GetByEmail(ctx, "a@b.com").Return(tt.repoUser, tt.repoErr)

			svc := services.NewUserService(repo, "secret", time.Hour)
			token, err := svc.Login(ctx, "a@b.com", tt.password)

			if tt.wantErr {
				if !errors.Is(err, user.ErrBadCreds) {
					t.Fatalf("expected ErrBadCreds, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if token == "" {
				t.Fatalf("expected a non-empty signed token")
			}
		})
	}
}
