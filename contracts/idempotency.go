//go:generate mockgen -source=idempotency.go -destination=mocks/mock_idempotency.go -package=mocks
package contracts

import "context"

type IdempotencyStore interface {
	Claim(ctx context.Context, key string) (ok bool, existing string, err error)
	Resolve(ctx context.Context, key, value string) error
	IsProcessing(value string) bool
}
