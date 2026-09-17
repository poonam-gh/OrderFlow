package routers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"orderflow/app"
	"orderflow/handlers"
	orderflowredis "orderflow/infra/redis"
	"orderflow/routers/middleware"
)

// Register wires every route onto engine: health check, metrics, and each
// domain handler's routes.
func Register(
	engine *gin.Engine,
	appCtx *app.AppContext,
	orderHandler *handlers.OrderHandler,
	productHandler *handlers.ProductHandler,
	userHandler *handlers.UserHandler,
) {
	limit := appCtx.Config.RateLimit.Requests
	window := time.Duration(appCtx.Config.RateLimit.WindowSeconds) * time.Second
	limiter := orderflowredis.NewRateLimiter(appCtx.Redis, limit, window)

	engine.Use(middleware.RequestID(), middleware.Metrics(), middleware.RateLimit(limiter))

	engine.GET("/healthz", healthz(appCtx))
	engine.GET("/metrics", gin.WrapH(promhttp.Handler()))

	jwtSecret := appCtx.Config.Auth.JWTSecret
	registerUserRoutes(engine, userHandler)
	registerProductRoutes(engine, productHandler, jwtSecret)
	registerOrderRoutes(engine, orderHandler, jwtSecret)
}

func healthz(appCtx *app.AppContext) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := appCtx.DB.PingContext(c.Request.Context()); err != nil {
			c.String(http.StatusServiceUnavailable, "db unreachable")
			return
		}
		if err := appCtx.Redis.Ping(c.Request.Context()).Err(); err != nil {
			c.String(http.StatusServiceUnavailable, "redis unreachable")
			return
		}
		c.Status(http.StatusOK)
	}
}
