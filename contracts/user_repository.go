package contracts

import (
	"context"

	"orderflow/domains/user"
)

type UserRepository interface {
	Create(ctx context.Context, u *user.User) error
	GetByEmail(ctx context.Context, email string) (*user.User, error)
}
