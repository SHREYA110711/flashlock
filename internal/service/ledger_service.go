package service

import (
	"context"
	"errors"
	"fmt"

	"flashlock/internal/domain/ledger"
	"flashlock/internal/repository/postgres"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var (
	ErrInvalidCurrency = errors.New("currency must be a 3-letter ISO code")
	ErrInvalidTransfer = errors.New("cannot transfer between identical accounts")
)

type LedgerService struct {
	repo *postgres.LedgerRepository
}

func NewLedgerService(repo *postgres.LedgerRepository) *LedgerService {
	return &LedgerService{repo: repo}
}

// CreateAccountRequest DTO
type CreateAccountRequest struct {
	Name     string             `json:"name"`
	Type     ledger.AccountType `json:"type"`
	Currency string             `json:"currency"`
}

// CreateAccount validates and creates a new account.
func (s *LedgerService) CreateAccount(ctx context.Context, req CreateAccountRequest) (*ledger.Account, error) {
	if req.Name == "" {
		return nil, errors.New("account name is required")
	}
	if !req.Type.IsValid() {
		return nil, ledger.ErrInvalidAccountType
	}
	if len(req.Currency) != 3 {
		return nil, ErrInvalidCurrency
	}

	acc := &ledger.Account{
		ID:       uuid.New(),
		Name:     req.Name,
		Type:     req.Type,
		Currency: req.Currency,
		Balance:  decimal.Zero,
	}

	if err := s.repo.CreateAccount(ctx, acc); err != nil {
		return nil, err
	}
	return acc, nil
}

// GetAccount retrieves account details and balance.
func (s *LedgerService) GetAccount(ctx context.Context, id uuid.UUID) (*ledger.Account, error) {
	return s.repo.GetAccountByID(ctx, id)
}

// PostTransactionRequest represents an arbitrary multi-legged financial journal entry.
type PostTransactionRequest struct {
	IdempotencyKey string                 `json:"idempotency_key"`
	Description    string                 `json:"description"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
	Entries        []EntryRequest         `json:"entries"`
}

type EntryRequest struct {
	AccountID uuid.UUID        `json:"account_id"`
	Direction ledger.Direction `json:"direction"`
	Amount    decimal.Decimal  `json:"amount"`
}

// PostJournalEntry validates and atomically records a multi-legged journal entry.
func (s *LedgerService) PostJournalEntry(ctx context.Context, req PostTransactionRequest) (*ledger.Transaction, error) {
	if req.IdempotencyKey == "" {
		return nil, ledger.ErrEmptyIdempotencyKey
	}
	if req.Description == "" {
		return nil, errors.New("description is required")
	}

	domainEntries := make([]ledger.Entry, len(req.Entries))
	for i, e := range req.Entries {
		domainEntries[i] = ledger.Entry{
			AccountID: e.AccountID,
			Direction: e.Direction,
			Amount:    e.Amount,
		}
	}

	tx := &ledger.Transaction{
		IdempotencyKey: req.IdempotencyKey,
		Description:    req.Description,
		Metadata:       req.Metadata,
		Entries:        domainEntries,
	}

	return s.repo.PostTransaction(ctx, tx)
}

// TransferRequest represents a standard 2-legged payment/transfer between two accounts.
type TransferRequest struct {
	FromAccountID  uuid.UUID       `json:"from_account_id"`
	ToAccountID    uuid.UUID       `json:"to_account_id"`
	Amount         decimal.Decimal `json:"amount"`
	Currency       string          `json:"currency"`
	IdempotencyKey string          `json:"idempotency_key"`
	Description    string          `json:"description"`
}

// Transfer executes a standard balanced transfer:
// Debits Destination Account, Credits Source Account (or vice-versa according to normal balance).
func (s *LedgerService) Transfer(ctx context.Context, req TransferRequest) (*ledger.Transaction, error) {
	if req.FromAccountID == req.ToAccountID {
		return nil, ErrInvalidTransfer
	}
	if !req.Amount.IsPositive() {
		return nil, ledger.ErrInvalidAmount
	}

	fromAcc, err := s.repo.GetAccountByID(ctx, req.FromAccountID)
	if err != nil {
		return nil, fmt.Errorf("source account: %w", err)
	}

	toAcc, err := s.repo.GetAccountByID(ctx, req.ToAccountID)
	if err != nil {
		return nil, fmt.Errorf("destination account: %w", err)
	}

	if fromAcc.Currency != toAcc.Currency {
		return nil, fmt.Errorf("%w: source=%s, dest=%s", ledger.ErrCurrencyMismatch, fromAcc.Currency, toAcc.Currency)
	}

	// For a transfer from Asset A to Asset B:
	// Credit Account A (decreases Asset A)
	// Debit Account B (increases Asset B)
	entries := []ledger.Entry{
		{
			AccountID: req.FromAccountID,
			Direction: ledger.DirectionCredit,
			Amount:    req.Amount,
		},
		{
			AccountID: req.ToAccountID,
			Direction: ledger.DirectionDebit,
			Amount:    req.Amount,
		},
	}

	tx := &ledger.Transaction{
		IdempotencyKey: req.IdempotencyKey,
		Description:    req.Description,
		Entries:        entries,
		Metadata: map[string]interface{}{
			"type":     "transfer",
			"currency": fromAcc.Currency,
		},
	}

	return s.repo.PostTransaction(ctx, tx)
}

func (s *LedgerService) GetTransaction(ctx context.Context, id uuid.UUID) (*ledger.Transaction, error) {
	return s.repo.GetTransactionByID(ctx, id)
}
