package redis

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

const processingPlaceholder = "processing"

// IdempotencyStore lets POST /orders be safely retried: a client sends the
// same Idempotency-Key on a retry and gets back the original result
// instead of creating a second order.
type IdempotencyStore struct {
	client *redis.Client
	ttl    time.Duration
}

func NewIdempotencyStore(client *redis.Client, ttl time.Duration) *IdempotencyStore {
	return &IdempotencyStore{client: client, ttl: ttl}
}

// Claim atomically reserves key via SETNX. ok=true means the caller should
// proceed to create the resource. ok=false means someone already claimed
// this key — existing is either "processing" (still in flight) or a
// previously resolved order id to replay.
func (s *IdempotencyStore) Claim(ctx context.Context, key string) (ok bool, existing string, err error) {
	fullKey := "idempotency:" + key

	set, err := s.client.SetNX(ctx, fullKey, processingPlaceholder, s.ttl).Result()
	if err != nil {
		return false, "", err
	}
	if set {
		return true, "", nil
	}

	val, err := s.client.Get(ctx, fullKey).Result()
	if errors.Is(err, redis.Nil) {
		// Claimed and expired/evicted between our SETNX and GET — rare, treat as a fresh claim.
		return true, "", nil
	}
	if err != nil {
		return false, "", err
	}
	return false, val, nil
}

func (s *IdempotencyStore) Resolve(ctx context.Context, key, orderID string) error {
	return s.client.Set(ctx, "idempotency:"+key, orderID, s.ttl).Err()
}

func (s *IdempotencyStore) IsProcessing(value string) bool {
	return value == processingPlaceholder
}
