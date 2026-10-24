package http

import (
	"net/http"

	"flashlock/internal/repository/postgres"

	"github.com/labstack/echo/v4"
)

type WebhookHandler struct {
	outboxRepo *postgres.OutboxRepository
	ledgerRepo *postgres.LedgerRepository
}

func NewWebhookHandler(outboxRepo *postgres.OutboxRepository, ledgerRepo *postgres.LedgerRepository) *WebhookHandler {
	return &WebhookHandler{
		outboxRepo: outboxRepo,
		ledgerRepo: ledgerRepo,
	}
}

func (h *WebhookHandler) RegisterRoutes(e *echo.Echo) {
	v1 := e.Group("/api/v1")

	// Webhook Subscriptions
	v1.POST("/webhooks", h.RegisterWebhook)
	v1.GET("/webhooks", h.ListWebhooks)

	// Cryptographic Audit Verification
	v1.GET("/ledger/audit/verify-chain", h.VerifyChain)
}

type RegisterWebhookRequest struct {
	URL        string   `json:"url"`
	EventTypes []string `json:"event_types"`
}

// RegisterWebhook handles POST /api/v1/webhooks
func (h *WebhookHandler) RegisterWebhook(c echo.Context) error {
	var req RegisterWebhookRequest
	if err := c.Bind(&req); err != nil || req.URL == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "valid webhook url is required"})
	}

	if len(req.EventTypes) == 0 {
		req.EventTypes = []string{"*"} // Subscribe to all events by default
	}

	sub := &postgres.WebhookSubscription{
		URL:        req.URL,
		EventTypes: req.EventTypes,
		IsActive:   true,
	}

	if err := h.outboxRepo.RegisterWebhook(c.Request().Context(), sub); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	return c.JSON(http.StatusCreated, sub)
}

// ListWebhooks handles GET /api/v1/webhooks
func (h *WebhookHandler) ListWebhooks(c echo.Context) error {
	subs, err := h.outboxRepo.GetActiveWebhooks(c.Request().Context())
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"webhooks": subs,
		"count":    len(subs),
	})
}

// VerifyChain handles GET /api/v1/ledger/audit/verify-chain
func (h *WebhookHandler) VerifyChain(c echo.Context) error {
	result, err := h.ledgerRepo.VerifyLedgerIntegrity(c.Request().Context())
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	status := http.StatusOK
	if !result.IsChainIntact {
		status = http.StatusConflict // 409 Conflict if cryptographic chain is broken!
	}

	return c.JSON(status, result)
}
