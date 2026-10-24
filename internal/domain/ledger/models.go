package ledger

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var (
	ErrImbalancedTransaction = errors.New("double-entry invariant violated: total debits must equal total credits")
	ErrInsufficientEntries   = errors.New("a double-entry transaction must have at least 2 entries")
	ErrInvalidAmount         = errors.New("entry amount must be strictly positive")
	ErrEmptyIdempotencyKey   = errors.New("idempotency key cannot be empty")
	ErrInvalidAccountType    = errors.New("invalid account type")
	ErrInvalidDirection      = errors.New("direction must be DEBIT or CREDIT")
	ErrCurrencyMismatch      = errors.New("all entries in a transaction must match the account currency")
)

type AccountType string

const (
	AccountTypeAsset     AccountType = "ASSET"     // Cash, Inventory, Accounts Receivable
	AccountTypeLiability AccountType = "LIABILITY" // Customer deposits, Accounts Payable
	AccountTypeEquity    AccountType = "EQUITY"    // Retained Earnings, Owner Capital
	AccountTypeRevenue   AccountType = "REVENUE"   // Sales, Processing Fees
	AccountTypeExpense   AccountType = "EXPENSE"   // Cost of Goods Sold, Server costs
)

func (t AccountType) IsValid() bool {
	switch t {
	case AccountTypeAsset, AccountTypeLiability, AccountTypeEquity, AccountTypeRevenue, AccountTypeExpense:
		return true
	default:
		return false
	}
}

type Direction string

const (
	DirectionDebit  Direction = "DEBIT"
	DirectionCredit Direction = "CREDIT"
)

func (d Direction) IsValid() bool {
	return d == DirectionDebit || d == DirectionCredit
}

// Account represents a financial ledger account.
type Account struct {
	ID        uuid.UUID       `json:"id"`
	Name      string          `json:"name"`
	Type      AccountType     `json:"type"`
	Currency  string          `json:"currency"`
	Balance   decimal.Decimal `json:"balance"`
	Version   int64           `json:"version"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// Entry is a single immutable debit or credit leg of a transaction.
type Entry struct {
	ID            uuid.UUID       `json:"id"`
	TransactionID uuid.UUID       `json:"transaction_id"`
	AccountID     uuid.UUID       `json:"account_id"`
	Direction     Direction       `json:"direction"`
	Amount        decimal.Decimal `json:"amount"`
	CreatedAt     time.Time       `json:"created_at"`
}

type TransactionStatus string

const (
	TransactionStatusPosted TransactionStatus = "POSTED"
	TransactionStatusVoided TransactionStatus = "VOIDED"
)

// Transaction represents a financial journal entry with multiple entries.
type Transaction struct {
	ID             uuid.UUID              `json:"id"`
	IdempotencyKey string                 `json:"idempotency_key"`
	Description    string                 `json:"description"`
	Status         TransactionStatus      `json:"status"`
	PreviousHash   string                 `json:"previous_hash,omitempty"`
	CurrentHash    string                 `json:"current_hash,omitempty"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
	Entries        []Entry                `json:"entries"`
	CreatedAt      time.Time              `json:"created_at"`
}

// ValidateTransaction enforces the fundamental accounting equation:
// sum(DEBITS) == sum(CREDITS)
func ValidateTransaction(entries []Entry) error {
	if len(entries) < 2 {
		return ErrInsufficientEntries
	}

	totalDebits := decimal.Zero
	totalCredits := decimal.Zero

	for idx, entry := range entries {
		if !entry.Direction.IsValid() {
			return fmt.Errorf("entry[%d]: %w", idx, ErrInvalidDirection)
		}

		if !entry.Amount.IsPositive() {
			return fmt.Errorf("entry[%d]: %w (amount: %s)", idx, ErrInvalidAmount, entry.Amount.String())
		}

		switch entry.Direction {
		case DirectionDebit:
			totalDebits = totalDebits.Add(entry.Amount)
		case DirectionCredit:
			totalCredits = totalCredits.Add(entry.Amount)
		}
	}

	if !totalDebits.Equal(totalCredits) {
		return fmt.Errorf("%w: total debits = %s, total credits = %s (diff = %s)",
			ErrImbalancedTransaction,
			totalDebits.String(),
			totalCredits.String(),
			totalDebits.Sub(totalCredits).String(),
		)
	}

	return nil
}
