package routers

import (
	"github.com/gin-gonic/gin"

	"orderflow/handlers"
	"orderflow/routers/middleware"
)

// Product creation is admin-only; reading a product is public.
func registerProductRoutes(engine *gin.Engine, h *handlers.ProductHandler, jwtSecret string) {
	engine.GET("/products", h.List)
	engine.GET("/products/:id", h.Get)
	engine.POST("/products",
		middleware.AuthRequired(jwtSecret),
		middleware.RequireRole("admin"),
		h.Create,
	)
}
