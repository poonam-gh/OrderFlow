package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"orderflow/contracts"
	"orderflow/domains/order"
	"orderflow/services"
)

const idempotencyKeyHeader = "Idempotency-Key"

type OrderHandler struct {
	service     *services.OrderService
	idempotency contracts.IdempotencyStore
}

func NewOrderHandler(service *services.OrderService, idempotency contracts.IdempotencyStore) *OrderHandler {
	return &OrderHandler{service: service, idempotency: idempotency}
}

type createOrderItemRequest struct {
	ProductID string `json:"product_id" binding:"required"`
	Quantity  int    `json:"quantity" binding:"required,gt=0"`
}

type createOrderRequest struct {
	Items []createOrderItemRequest `json:"items" binding:"required,min=1,dive"`
}

func (h *OrderHandler) Create(c *gin.Context) {
	var req createOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	idemKey := c.GetHeader(idempotencyKeyHeader)
	if idemKey != "" {
		if _, done := h.replayIfClaimed(c, idemKey); done {
			return
		}
	}

	items := make([]order.ItemInput, len(req.Items))
	for i, it := range req.Items {
		items[i] = order.ItemInput{ProductID: it.ProductID, Quantity: it.Quantity}
	}

	userID := c.GetString("user_id")
	o, err := h.service.CreateOrder(c.Request.Context(), userID, items)
	switch {
	case errors.Is(err, order.ErrProductNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, order.ErrInsufficientStock), errors.Is(err, order.ErrEmptyItems):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case err != nil:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	default:
		if idemKey != "" {
			_ = h.idempotency.Resolve(c.Request.Context(), idemKey, o.ID)
		}
		c.JSON(http.StatusCreated, o)
	}
}

// replayIfClaimed checks the Idempotency-Key: if this is a fresh key it
// claims it and lets Create proceed (done=false); if it's a retry of an
// in-flight or already-completed request, it writes the appropriate
// response itself and tells Create to stop (done=true).
func (h *OrderHandler) replayIfClaimed(c *gin.Context, key string) (o *order.Order, done bool) {
	claimed, existing, err := h.idempotency.Claim(c.Request.Context(), key)
	if err != nil {
		// Redis hiccup: fail open and let the request proceed as non-idempotent
		// rather than blocking order creation entirely.
		return nil, false
	}
	if claimed {
		return nil, false
	}

	if h.idempotency.IsProcessing(existing) {
		c.JSON(http.StatusConflict, gin.H{"error": "a request with this idempotency key is already in progress"})
		return nil, true
	}

	userID := c.GetString("user_id")
	prior, err := h.service.GetOrder(c.Request.Context(), existing, userID, c.GetString("role") == "admin")
	if err != nil {
		// The claimed order id no longer resolves (e.g. TTL edge case) — let it proceed fresh.
		return nil, false
	}
	c.JSON(http.StatusOK, prior)
	return prior, true
}

func (h *OrderHandler) Get(c *gin.Context) {
	userID := c.GetString("user_id")
	isAdmin := c.GetString("role") == "admin"

	o, err := h.service.GetOrder(c.Request.Context(), c.Param("id"), userID, isAdmin)
	switch {
	case errors.Is(err, order.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "order not found"})
	case errors.Is(err, order.ErrForbidden):
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
	case err != nil:
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusOK, o)
	}
}

func (h *OrderHandler) List(c *gin.Context) {
	limit, offset := paginationParams(c)

	orders, err := h.service.ListMyOrders(c.Request.Context(), c.GetString("user_id"), limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"orders": orders, "limit": limit, "offset": offset})
}

func paginationParams(c *gin.Context) (limit, offset int) {
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if err != nil || limit <= 0 || limit > 100 {
		limit = 20
	}
	offset, err = strconv.Atoi(c.DefaultQuery("offset", "0"))
	if err != nil || offset < 0 {
		offset = 0
	}
	return limit, offset
}
