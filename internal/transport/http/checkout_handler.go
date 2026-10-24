package http

import (
	"errors"
	"net/http"

	"flashlock/internal/service"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

type CheckoutHandler struct {
	orchestrator       *service.SagaOrchestrator
	reservationService *service.ReservationService
}

func NewCheckoutHandler(
	orchestrator *service.SagaOrchestrator,
	reservationService *service.ReservationService,
) *CheckoutHandler {
	return &CheckoutHandler{
		orchestrator:       orchestrator,
		reservationService: reservationService,
	}
}

func (h *CheckoutHandler) RegisterRoutes(e *echo.Echo) {
	v1 := e.Group("/api/v1")

	v1.POST("/checkout", h.Checkout)
	v1.POST("/checkout/cancel", h.Cancel)
}

// Checkout handles POST /api/v1/checkout (Two-Phase Hold + Settle + Post Saga)
func (h *CheckoutHandler) Checkout(c echo.Context) error {
	var req service.CheckoutRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid checkout payload"})
	}

	result, err := h.orchestrator.CheckoutHold(c.Request().Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrHoldExpired):
			return c.JSON(http.StatusGone, map[string]string{
				"error":   "hold_expired",
				"message": "the reservation hold expired before checkout could be completed",
			})
		case errors.Is(err, service.ErrHoldNotEligibleForCheckout):
			return c.JSON(http.StatusConflict, map[string]string{
				"error":   "invalid_hold_state",
				"message": err.Error(),
			})
		case errors.Is(err, service.ErrCustomerAccountMismatch):
			return c.JSON(http.StatusUnprocessableEntity, map[string]string{
				"error":   "currency_mismatch",
				"message": err.Error(),
			})
		default:
			return c.JSON(http.StatusBadRequest, map[string]string{
				"error":   "checkout_failed",
				"message": err.Error(),
			})
		}
	}

	return c.JSON(http.StatusOK, result)
}

type CancelCheckoutRequest struct {
	HoldID uuid.UUID `json:"hold_id"`
}

// Cancel handles POST /api/v1/checkout/cancel
func (h *CheckoutHandler) Cancel(c echo.Context) error {
	var req CancelCheckoutRequest
	if err := c.Bind(&req); err != nil || req.HoldID == uuid.Nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid cancel payload or hold_id"})
	}

	if err := h.reservationService.ReleaseHold(c.Request().Context(), req.HoldID); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, map[string]string{
		"status":  "cancelled",
		"message": "checkout cancelled and reservation hold released back to inventory",
	})
}
