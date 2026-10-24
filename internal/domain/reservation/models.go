package reservation

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var (
	ErrHoldExpired          = errors.New("reservation hold has expired")
	ErrHoldAlreadyProcessed = errors.New("reservation hold has already been confirmed or released")
	ErrInsufficientStock    = errors.New("insufficient inventory available for reservation")
	ErrInvalidHoldQuantity  = errors.New("reservation quantity must be greater than zero")
)

type HoldStatus string

const (
	HoldStatusPending   HoldStatus = "PENDING"
	HoldStatusConfirmed HoldStatus = "CONFIRMED"
	HoldStatusExpired   HoldStatus = "EXPIRED"
	HoldStatusReleased  HoldStatus = "RELEASED"
)

func (s HoldStatus) IsTerminal() bool {
	return s == HoldStatusConfirmed || s == HoldStatusExpired || s == HoldStatusReleased
}

// InventoryItem represents a bookable/purchasable asset.
type InventoryItem struct {
	ID            uuid.UUID       `json:"id"`
	SKU           string          `json:"sku"`
	Name          string          `json:"name"`
	TotalStock    int             `json:"total_stock"`
	ReservedStock int             `json:"reserved_stock"`
	Price         decimal.Decimal `json:"price"`
	Currency      string          `json:"currency"`
	Version       int64           `json:"version"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

func (i *InventoryItem) AvailableStock() int {
	avail := i.TotalStock - i.ReservedStock
	if avail < 0 {
		return 0
	}
	return avail
}

// Hold represents a temporary leased reservation on stock.
type Hold struct {
	ID             uuid.UUID  `json:"id"`
	InventoryID    uuid.UUID  `json:"inventory_id"`
	CustomerID     string     `json:"customer_id"`
	IdempotencyKey string     `json:"idempotency_key"`
	Quantity       int        `json:"quantity"`
	Status         HoldStatus `json:"status"`
	ExpiresAt      time.Time  `json:"expires_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

func (h *Hold) IsExpired() bool {
	return time.Now().After(h.ExpiresAt)
}
