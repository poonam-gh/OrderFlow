package routers

import (
	"github.com/gin-gonic/gin"

	"orderflow/handlers"
	"orderflow/routers/middleware"
)

// Both order routes require a logged-in user; CreateOrder reads the user
// id from the JWT (set by AuthRequired) rather than trusting a client-
// supplied field, so an order can never be created for someone else.
func registerOrderRoutes(engine *gin.Engine, h *handlers.OrderHandler, jwtSecret string) {
	authed := engine.Group("/", middleware.AuthRequired(jwtSecret))
	authed.POST("/orders", h.Create)
	authed.GET("/orders", h.List)
	authed.GET("/orders/:id", h.Get)
}
