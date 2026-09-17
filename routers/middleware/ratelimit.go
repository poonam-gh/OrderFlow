package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"

	orderflowredis "orderflow/infra/redis"
)

func RateLimit(limiter *orderflowredis.RateLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		allowed, err := limiter.Allow(c.Request.Context(), c.ClientIP())
		if err != nil {
			// Redis hiccup shouldn't take the whole API down — fail open.
			c.Next()
			return
		}
		if !allowed {
			c.Header("Retry-After", "60")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
			return
		}
		c.Next()
	}
}
