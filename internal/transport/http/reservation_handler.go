package http

import (
	"errors"
	"net/http"
	"strconv"

	"flashlock/internal/domain/reservation"
	"flashlock/internal/repository/postgres"
	redispkg "flashlock/internal/repository/redis"
	"flashlock/internal/service"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

type ReservationHandler struct {
	service *service.ReservationService
}

func NewReservationHandler(service *service.ReservationService) *ReservationHandler {
	return &ReservationHandler{service: service}
}

func (h *ReservationHandler) RegisterRoutes(e *echo.Echo) {
	v1 := e.Group("/api/v1")

	// Inventory
	v1.POST("/inventory", h.CreateInventory)
	v1.GET("/inventory/:sku", h.GetInventory)
	v1.GET("/inventory/:sku/holds", h.GetInventoryHolds)

	// Holds
	v1.POST("/reservations/hold", h.HoldInventory)
	v1.POST("/reservations/:id/confirm", h.ConfirmHold)
	v1.POST("/reservations/:id/release", h.ReleaseHold)
}

// CreateInventory handles POST /api/v1/inventory
func (h *ReservationHandler) CreateInventory(c echo.Context) error {
	var req service.CreateInventoryRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid inventory payload"})
	}

	item, err := h.service.CreateInventory(c.Request().Context(), req)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}

	return c.JSON(http.StatusCreated, item)
}

// GetInventory handles GET /api/v1/inventory/:sku
func (h *ReservationHandler) GetInventory(c echo.Context) error {
	sku := c.Param("sku")
	if sku == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "SKU parameter is required"})
	}

	item, liveStock, err := h.service.GetInventory(c.Request().Context(), sku)
	if err != nil {
		if errors.Is(err, postgres.ErrInventoryNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "inventory SKU not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"item":            item,
		"available_stock": liveStock,
	})
}

// HoldInventory handles POST /api/v1/reservations/hold
func (h *ReservationHandler) HoldInventory(c echo.Context) error {
	var req service.HoldRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid hold payload"})
	}

	hold, err := h.service.HoldInventory(c.Request().Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, redispkg.ErrInsufficientStock), errors.Is(err, reservation.ErrInsufficientStock):
			return c.JSON(http.StatusConflict, map[string]string{
				"error":   "insufficient_stock",
				"message": "not enough inventory available to fulfill reservation hold",
			})
		case errors.Is(err, postgres.ErrInventoryNotFound):
			return c.JSON(http.StatusNotFound, map[string]string{"error": "inventory SKU not found"})
		default:
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
	}

	return c.JSON(http.StatusCreated, hold)
}

// ConfirmHold handles POST /api/v1/reservations/:id/confirm
func (h *ReservationHandler) ConfirmHold(c echo.Context) error {
	idParam := c.Param("id")
	holdID, err := uuid.Parse(idParam)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid hold UUID"})
	}

	hold, err := h.service.ConfirmHold(c.Request().Context(), holdID)
	if err != nil {
		switch {
		case errors.Is(err, postgres.ErrHoldNotFoundInDB), errors.Is(err, redispkg.ErrHoldNotFound):
			return c.JSON(http.StatusNotFound, map[string]string{"error": "hold not found"})
		case errors.Is(err, service.ErrHoldExpiredAlready):
			return c.JSON(http.StatusGone, map[string]string{"error": "hold expired"})
		default:
			return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
	}

	return c.JSON(http.StatusOK, hold)
}

// ReleaseHold handles POST /api/v1/reservations/:id/release
func (h *ReservationHandler) ReleaseHold(c echo.Context) error {
	idParam := c.Param("id")
	holdID, err := uuid.Parse(idParam)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid hold UUID"})
	}

	if err := h.service.ReleaseHold(c.Request().Context(), holdID); err != nil {
		if errors.Is(err, postgres.ErrHoldNotFoundInDB) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "hold not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, map[string]string{
		"status":  "released",
		"message": "reservation hold released successfully and stock restored",
	})
}

// GetInventoryHolds handles GET /api/v1/inventory/:sku/holds?limit=50&offset=0
func (h *ReservationHandler) GetInventoryHolds(c echo.Context) error {
	sku := c.Param("sku")
	if sku == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "SKU parameter is required"})
	}

	limit, _ := strconv.Atoi(c.QueryParam("limit"))
	offset, _ := strconv.Atoi(c.QueryParam("offset"))

	holds, err := h.service.GetInventoryHolds(c.Request().Context(), sku, limit, offset)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"sku":   sku,
		"holds": holds,
		"count": len(holds),
	})
}

