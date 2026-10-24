package http

import (
	"errors"
	"net/http"
	"time"

	"flashlock/internal/domain/ledger"
	"flashlock/internal/repository/postgres"
	"flashlock/internal/service"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

type LedgerHandler struct {
	service *service.LedgerService
}

func NewLedgerHandler(service *service.LedgerService) *LedgerHandler {
	return &LedgerHandler{service: service}
}

func (h *LedgerHandler) RegisterRoutes(e *echo.Echo) {
	v1 := e.Group("/api/v1")

	// Accounts & Statements
	v1.POST("/accounts", h.CreateAccount)
	v1.GET("/accounts/:id", h.GetAccount)
	v1.GET("/accounts/:id/statement", h.GetAccountStatement)

	// Transactions & Transfers
	v1.POST("/ledger/transactions", h.PostTransaction)
	v1.GET("/ledger/transactions/:id", h.GetTransaction)
	v1.POST("/ledger/transfers", h.Transfer)
	v1.GET("/ledger/trial-balance", h.GetTrialBalance)
}

// CreateAccount handles POST /api/v1/accounts
func (h *LedgerHandler) CreateAccount(c echo.Context) error {
	var req service.CreateAccountRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request payload"})
	}

	acc, err := h.service.CreateAccount(c.Request().Context(), req)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}

	return c.JSON(http.StatusCreated, acc)
}

// GetAccount handles GET /api/v1/accounts/:id
func (h *LedgerHandler) GetAccount(c echo.Context) error {
	idParam := c.Param("id")
	id, err := uuid.Parse(idParam)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid account UUID"})
	}

	acc, err := h.service.GetAccount(c.Request().Context(), id)
	if err != nil {
		if errors.Is(err, postgres.ErrAccountNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "account not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, acc)
}

// PostTransaction handles POST /api/v1/ledger/transactions
func (h *LedgerHandler) PostTransaction(c echo.Context) error {
	var req service.PostTransactionRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid transaction payload"})
	}

	tx, err := h.service.PostJournalEntry(c.Request().Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, ledger.ErrImbalancedTransaction),
			errors.Is(err, ledger.ErrInsufficientEntries),
			errors.Is(err, ledger.ErrInvalidAmount),
			errors.Is(err, ledger.ErrEmptyIdempotencyKey),
			errors.Is(err, ledger.ErrInvalidDirection):
			return c.JSON(http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		case errors.Is(err, postgres.ErrAccountNotFound):
			return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		case errors.Is(err, postgres.ErrInsufficientFunds):
			return c.JSON(http.StatusConflict, map[string]string{"error": err.Error()})
		default:
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
	}

	return c.JSON(http.StatusCreated, tx)
}

// GetTransaction handles GET /api/v1/ledger/transactions/:id
func (h *LedgerHandler) GetTransaction(c echo.Context) error {
	idParam := c.Param("id")
	id, err := uuid.Parse(idParam)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid transaction UUID"})
	}

	tx, err := h.service.GetTransaction(c.Request().Context(), id)
	if err != nil {
		if errors.Is(err, postgres.ErrTransactionNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "transaction not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, tx)
}

// Transfer handles POST /api/v1/ledger/transfers
func (h *LedgerHandler) Transfer(c echo.Context) error {
	var req service.TransferRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid transfer payload"})
	}

	tx, err := h.service.Transfer(c.Request().Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidTransfer),
			errors.Is(err, ledger.ErrInvalidAmount),
			errors.Is(err, ledger.ErrCurrencyMismatch):
			return c.JSON(http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		case errors.Is(err, postgres.ErrAccountNotFound):
			return c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		case errors.Is(err, postgres.ErrInsufficientFunds):
			return c.JSON(http.StatusConflict, map[string]string{"error": err.Error()})
		default:
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
	}

	return c.JSON(http.StatusCreated, tx)
}

// GetAccountStatement handles GET /api/v1/accounts/:id/statement?start_date=...&end_date=...
func (h *LedgerHandler) GetAccountStatement(c echo.Context) error {
	idParam := c.Param("id")
	id, err := uuid.Parse(idParam)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid account UUID"})
	}

	var startDate, endDate time.Time
	if startParam := c.QueryParam("start_date"); startParam != "" {
		if t, err := time.Parse(time.RFC3339, startParam); err == nil {
			startDate = t
		}
	}
	if endParam := c.QueryParam("end_date"); endParam != "" {
		if t, err := time.Parse(time.RFC3339, endParam); err == nil {
			endDate = t
		}
	}

	statement, err := h.service.GenerateStatement(c.Request().Context(), id, startDate, endDate)
	if err != nil {
		if errors.Is(err, postgres.ErrAccountNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "account not found"})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, statement)
}

// GetTrialBalance handles GET /api/v1/ledger/trial-balance
func (h *LedgerHandler) GetTrialBalance(c echo.Context) error {
	report, err := h.service.GenerateTrialBalance(c.Request().Context())
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	return c.JSON(http.StatusOK, report)
}

