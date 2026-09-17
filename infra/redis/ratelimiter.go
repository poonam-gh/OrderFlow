package redis

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// RateLimiter is a fixed-window counter: INCR is atomic on Redis's single
// command thread, so concurrent requests never race on the check-then-set
// step the way they would with a naive GET-then-SET in application code.
type RateLimiter struct {
	client *redis.Client
	limit  int
	window time.Duration
}

func NewRateLimiter(client *redis.Client, limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{client: client, limit: limit, window: window}
}

func (r *RateLimiter) Allow(ctx context.Context, key string) (bool, error) {
	fullKey := "ratelimit:" + key

	count, err := r.client.Incr(ctx, fullKey).Result()
	if err != nil {
		return false, err
	}
	if count == 1 {
		r.client.Expire(ctx, fullKey, r.window)
	}

	return count <= int64(r.limit), nil
}
