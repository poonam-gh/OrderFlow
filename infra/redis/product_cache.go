package redis

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"

	"orderflow/domains/product"
)

type ProductCache struct {
	client *redis.Client
	ttl    time.Duration
}

func NewProductCache(client *redis.Client, ttl time.Duration) *ProductCache {
	return &ProductCache{client: client, ttl: ttl}
}

func (c *ProductCache) Get(ctx context.Context, id string) (*product.Product, bool) {
	data, err := c.client.Get(ctx, "product:"+id).Bytes()
	if err != nil {
		return nil, false
	}

	var p product.Product
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, false
	}
	return &p, true
}

func (c *ProductCache) Set(ctx context.Context, p *product.Product) {
	data, err := json.Marshal(p)
	if err != nil {
		return
	}
	c.client.Set(ctx, "product:"+p.ID, data, c.ttl)
}
