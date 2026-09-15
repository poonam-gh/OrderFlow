package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"orderflow/services"
)

type OrderHandler struct {
	service *services.OrderService
}

func NewOrderHandler(service *services.OrderService) *OrderHandler {
	return &OrderHandler{service: service}
}

type createOrderRequest struct {
	UserID     string `json:"user_id"`
	TotalCents int64  `json:"total_cents"`
}

func (h *OrderHandler) Create(c *gin.Context) {
	var req createOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	o, err := h.service.CreateOrder(c.Request.Context(), req.UserID, req.TotalCents)
	if err != nil {
		if errors.Is(err, services.ErrUserNotFound) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "user does not exist"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, o)
}

func (h *OrderHandler) Register(r *gin.Engine) {
	r.POST("/orders", h.Create)
}
