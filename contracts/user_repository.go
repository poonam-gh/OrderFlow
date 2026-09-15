package contracts

import "context"

type UserRepository interface {
	ExistsByID(ctx context.Context, id string) (bool, error)
}
