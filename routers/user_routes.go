package routers

import (
	"github.com/gin-gonic/gin"

	"orderflow/handlers"
)

func registerUserRoutes(engine *gin.Engine, h *handlers.UserHandler) {
	engine.POST("/users/register", h.Register)
	engine.POST("/users/login", h.Login)
}
