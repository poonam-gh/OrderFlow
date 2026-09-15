package routers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"orderflow/app"
	"orderflow/handlers"
)

// Register wires every route onto engine: health check plus each domain handler's routes.
func Register(engine *gin.Engine, appCtx *app.AppContext, orderHandler *handlers.OrderHandler) {
	engine.GET("/healthz", healthz(appCtx))

	registerOrderRoutes(engine, orderHandler)
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
