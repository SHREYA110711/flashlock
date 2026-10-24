package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"flashlock/internal/domain/ledger"
	"flashlock/internal/domain/reservation"
	"flashlock/internal/repository/postgres"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var (
	ErrHoldNotEligibleForCheckout = errors.New("hold is not eligible for checkout")
	ErrHoldExpired                = errors.New("hold expired before checkout completed")
	ErrCustomerAccountMismatch    = errors.New("customer account currency mismatch")
)

type CheckoutRequest struct {
	HoldID            uuid.UUID `json:"hold_id"`
	CustomerAccountID uuid.UUID `json:"customer_account_id"`
	MerchantAccountID uuid.UUID `json:"merchant_account_id"`
	IdempotencyKey    string    `json:"idempotency_key"`
}

type CheckoutResult struct {
	HoldID        uuid.UUID          `json:"hold_id"`
	TransactionID uuid.UUID          `json:"transaction_id"`
	SKU           string             `json:"sku"`
	Quantity      int                `json:"quantity"`
	TotalAmount   decimal.Decimal    `json:"total_amount"`
	Currency      string             `json:"currency"`
	Status        string             `json:"status"`
	CompletedAt   time.Time          `json:"completed_at"`
}

type SagaOrchestrator struct {
	ledgerService      *LedgerService
	reservationService *ReservationService
	inventoryRepo      *postgres.InventoryRepository
	ledgerRepo         *postgres.LedgerRepository
}

func NewSagaOrchestrator(
	ledgerService *LedgerService,
	reservationService *ReservationService,
	inventoryRepo *postgres.InventoryRepository,
	ledgerRepo *postgres.LedgerRepository,
) *SagaOrchestrator {
	return &SagaOrchestrator{
		ledgerService:      ledgerService,
		reservationService: reservationService,
		inventoryRepo:      inventoryRepo,
		ledgerRepo:         ledgerRepo,
	}
}

// CheckoutHold coordinates the 2-phase saga:
// 1. Validates hold status and expiration.
// 2. Confirms inventory hold (permanent stock deduction).
// 3. Atomically posts balanced financial entries in the double-entry ledger.
// 4. If ledger posting fails, compensates by restoring inventory.
func (s *SagaOrchestrator) CheckoutHold(ctx context.Context, req CheckoutRequest) (*CheckoutResult, error) {
	if req.HoldID == uuid.Nil {
		return nil, errors.New("hold_id is required")
	}
	if req.CustomerAccountID == uuid.Nil {
		return nil, errors.New("customer_account_id is required")
	}
	if req.MerchantAccountID == uuid.Nil {
		return nil, errors.New("merchant_account_id is required")
	}
	if req.IdempotencyKey == "" {
		return nil, errors.New("idempotency_key is required")
	}

	// Step 1: Idempotency Check in Ledger
	existingTx, err := s.ledgerRepo.GetTransactionByIdempotencyKey(ctx, req.IdempotencyKey)
	if err == nil && existingTx != nil {
		// Return idempotent response
		totalAmount := decimal.Zero
		for _, e := range existingTx.Entries {
			if e.Direction == ledger.DirectionCredit {
				totalAmount = totalAmount.Add(e.Amount)
			}
		}
		return &CheckoutResult{
			HoldID:        req.HoldID,
			TransactionID: existingTx.ID,
			TotalAmount:   totalAmount,
			Status:        "SETTLED",
			CompletedAt:   existingTx.CreatedAt,
		}, nil
	}

	// Step 2: Fetch and validate Hold
	hold, err := s.inventoryRepo.GetHoldByID(ctx, req.HoldID)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve hold: %w", err)
	}

	if hold.Status != reservation.HoldStatusPending {
		return nil, fmt.Errorf("%w: current status is %s", ErrHoldNotEligibleForCheckout, hold.Status)
	}

	if hold.IsExpired() {
		// Auto-mark expired in DB and release
		_ = s.reservationService.ReleaseHold(ctx, req.HoldID)
		return nil, ErrHoldExpired
	}

	// Step 3: Fetch Item details and calculate price
	item, err := s.inventoryRepo.GetItemByID(ctx, hold.InventoryID)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve inventory item: %w", err)
	}

	totalAmount := item.Price.Mul(decimal.NewFromInt(int64(hold.Quantity)))

	// Step 4: Verify accounts exist and match currencies
	custAcc, err := s.ledgerRepo.GetAccountByID(ctx, req.CustomerAccountID)
	if err != nil {
		return nil, fmt.Errorf("customer account error: %w", err)
	}
	merchAcc, err := s.ledgerRepo.GetAccountByID(ctx, req.MerchantAccountID)
	if err != nil {
		return nil, fmt.Errorf("merchant account error: %w", err)
	}

	if custAcc.Currency != item.Currency || merchAcc.Currency != item.Currency {
		return nil, fmt.Errorf("%w: item=%s, customer=%s, merchant=%s",
			ErrCustomerAccountMismatch, item.Currency, custAcc.Currency, merchAcc.Currency)
	}

	// Step 5: Phase 1 of Saga - Confirm Hold
	confirmedHold, err := s.reservationService.ConfirmHold(ctx, req.HoldID)
	if err != nil {
		return nil, fmt.Errorf("failed to confirm reservation hold: %w", err)
	}

	// Step 6: Phase 2 of Saga - Post Double-Entry Ledger Transaction
	// Customer Account (Asset): Credit to reduce balance
	// Merchant Account (Revenue): Credit to increase revenue
	// Or Customer Account (Asset): Credit, Merchant Account (Asset): Debit
	// Standard payment between two accounts:
	// Debit Merchant Cash/Asset (increases Asset)
	// Credit Customer Cash/Asset (decreases Asset)
	txEntries := []ledger.Entry{
		{
			AccountID: req.CustomerAccountID,
			Direction: ledger.DirectionCredit, // Customer balance decreases
			Amount:    totalAmount,
		},
		{
			AccountID: req.MerchantAccountID,
			Direction: ledger.DirectionDebit, // Merchant balance increases
			Amount:    totalAmount,
		},
	}

	ledgerTx := &ledger.Transaction{
		IdempotencyKey: req.IdempotencyKey,
		Description:    fmt.Sprintf("Payment for %d x %s (Hold %s)", hold.Quantity, item.SKU, hold.ID),
		Entries:        txEntries,
		Metadata: map[string]interface{}{
			"hold_id":     hold.ID.String(),
			"sku":         item.SKU,
			"quantity":    hold.Quantity,
			"customer_id": hold.CustomerID,
		},
	}

	postedTx, err := s.ledgerRepo.PostTransaction(ctx, ledgerTx)
	if err != nil {
		// ====================================================================
		// SAGA COMPENSATION: Ledger post failed (e.g. Insufficient customer funds)
		// We must rollback the reservation hold so inventory is restored!
		// ====================================================================
		_ = s.reservationService.ReleaseHold(ctx, req.HoldID)
		return nil, fmt.Errorf("settlement failed, reservation compensated/released: %w", err)
	}

	return &CheckoutResult{
		HoldID:        confirmedHold.ID,
		TransactionID: postedTx.ID,
		SKU:           item.SKU,
		Quantity:      hold.Quantity,
		TotalAmount:   totalAmount,
		Currency:      item.Currency,
		Status:        "SETTLED",
		CompletedAt:   postedTx.CreatedAt,
	}, nil
}
