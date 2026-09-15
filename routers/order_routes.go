package routers

import (
	"github.com/gin-gonic/gin"

	"orderflow/handlers"
)

func registerOrderRoutes(engine *gin.Engine, h *handlers.OrderHandler) {
	engine.POST("/orders", h.Create)
	engine.GET("/orders/:id", h.Get)
}
