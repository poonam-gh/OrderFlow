package middleware

import (
	"github.com/gin-gonic/gin"

	"orderflow/pkg/requestid"
)

const requestIDHeader = "X-Request-ID"

// RequestID takes the caller's X-Request-ID if present, otherwise
// generates one, puts it on the request's context.Context (so it flows
// down through handler -> service -> repository, and from there into an
// outbox event payload for the worker to pick back up), and echoes it on
// the response.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(requestIDHeader)
		if id == "" {
			id = requestid.New()
		}

		ctx := requestid.WithID(c.Request.Context(), id)
		c.Request = c.Request.WithContext(ctx)

		c.Header(requestIDHeader, id)
		c.Next()
	}
}
