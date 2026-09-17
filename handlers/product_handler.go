package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"orderflow/domains/product"
	"orderflow/services"
)

type ProductHandler struct {
	service *services.ProductService
}

func NewProductHandler(service *services.ProductService) *ProductHandler {
	return &ProductHandler{service: service}
}

type createProductRequest struct {
	Name       string `json:"name" binding:"required"`
	PriceCents int64  `json:"price_cents" binding:"required,gt=0"`
	Stock      int    `json:"stock" binding:"gte=0"`
}

func (h *ProductHandler) Create(c *gin.Context) {
	var req createProductRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	p, err := h.service.CreateProduct(c.Request.Context(), req.Name, req.PriceCents, req.Stock)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, p)
}

func (h *ProductHandler) Get(c *gin.Context) {
	p, err := h.service.GetProduct(c.Request.Context(), c.Param("id"))
	if errors.Is(err, product.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "product not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, p)
}

func (h *ProductHandler) List(c *gin.Context) {
	limit, offset := paginationParams(c)

	products, err := h.service.ListProducts(c.Request.Context(), limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"products": products, "limit": limit, "offset": offset})
}
