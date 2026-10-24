package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"flashlock/internal/domain/reservation"
	"flashlock/internal/repository/postgres"
	redispkg "flashlock/internal/repository/redis"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var (
	ErrHoldExpiredAlready = errors.New("cannot confirm expired hold")
)

type ReservationService struct {
	pgRepo        *postgres.InventoryRepository
	redisRepo     *redispkg.ReservationRedisRepository
	defaultHoldTTL int
}

func NewReservationService(
	pgRepo *postgres.InventoryRepository,
	redisRepo *redispkg.ReservationRedisRepository,
	defaultHoldTTL int,
) *ReservationService {
	if defaultHoldTTL <= 0 {
		defaultHoldTTL = 600 // 10 minutes default
	}
	return &ReservationService{
		pgRepo:         pgRepo,
		redisRepo:      redisRepo,
		defaultHoldTTL: defaultHoldTTL,
	}
}

// CreateInventoryRequest DTO
type CreateInventoryRequest struct {
	SKU        string          `json:"sku"`
	Name       string          `json:"name"`
	TotalStock int             `json:"total_stock"`
	Price      decimal.Decimal `json:"price"`
	Currency   string          `json:"currency"`
}

// CreateInventory creates a new inventory item and seeds Redis atomic stock.
func (s *ReservationService) CreateInventory(ctx context.Context, req CreateInventoryRequest) (*reservation.InventoryItem, error) {
	if req.SKU == "" {
		return nil, errors.New("SKU is required")
	}
	if req.TotalStock < 0 {
		return nil, errors.New("total stock cannot be negative")
	}
	if !req.Price.IsPositive() {
		return nil, errors.New("price must be positive")
	}
	if req.Currency == "" {
		req.Currency = "USD"
	}

	item := &reservation.InventoryItem{
		ID:            uuid.New(),
		SKU:           req.SKU,
		Name:          req.Name,
		TotalStock:    req.TotalStock,
		ReservedStock: 0,
		Price:         req.Price,
		Currency:      req.Currency,
	}

	// 1. Persist to PostgreSQL
	if err := s.pgRepo.CreateItem(ctx, item); err != nil {
		return nil, err
	}

	// 2. Pre-seed Redis atomic stock counter
	if s.redisRepo != nil {
		if err := s.redisRepo.SetInitialStock(ctx, item.SKU, item.TotalStock); err != nil {
			return nil, fmt.Errorf("failed to initialize redis stock: %w", err)
		}
	}

	return item, nil
}

// GetInventory retrieves item and live stock from Redis cache.
func (s *ReservationService) GetInventory(ctx context.Context, sku string) (*reservation.InventoryItem, int, error) {
	item, err := s.pgRepo.GetItemBySKU(ctx, sku)
	if err != nil {
		return nil, 0, err
	}

	liveStock := item.TotalStock
	if s.redisRepo != nil {
		if stock, err := s.redisRepo.GetAvailableStock(ctx, sku); err == nil {
			liveStock = stock
		}
	}

	return item, liveStock, nil
}

// HoldRequest DTO
type HoldRequest struct {
	SKU            string `json:"sku"`
	CustomerID     string `json:"customer_id"`
	IdempotencyKey string `json:"idempotency_key"`
	Quantity       int    `json:"quantity"`
	TTLSeconds     int    `json:"ttl_seconds,omitempty"`
}

// HoldInventory executes high-speed atomic hold allocation in Redis with Postgres persistence.
func (s *ReservationService) HoldInventory(ctx context.Context, req HoldRequest) (*reservation.Hold, error) {
	if req.SKU == "" {
		return nil, errors.New("SKU is required")
	}
	if req.CustomerID == "" {
		return nil, errors.New("customer_id is required")
	}
	if req.IdempotencyKey == "" {
		return nil, errors.New("idempotency_key is required")
	}
	if req.Quantity <= 0 {
		return nil, reservation.ErrInvalidHoldQuantity
	}

	ttl := req.TTLSeconds
	if ttl <= 0 {
		ttl = s.defaultHoldTTL
	}

	// 1. Verify item exists in Postgres
	item, err := s.pgRepo.GetItemBySKU(ctx, req.SKU)
	if err != nil {
		return nil, err
	}

	// 2. Check Idempotency Key in PostgreSQL
	existingHold, err := s.pgRepo.GetHoldByIdempotencyKey(ctx, req.IdempotencyKey)
	if err == nil && existingHold != nil {
		return existingHold, nil // Idempotent response
	}

	holdID := uuid.New()

	// 3. Fast-path: Atomic check-and-decrement in Redis via Lua Script
	var expiresAt time.Time
	if s.redisRepo != nil {
		exp, err := s.redisRepo.ReserveHold(ctx, holdID, req.SKU, req.CustomerID, req.Quantity, ttl)
		if err != nil {
			return nil, err
		}
		expiresAt = exp
	} else {
		expiresAt = time.Now().UTC().Add(time.Duration(ttl) * time.Second)
	}

	// 4. Record hold in PostgreSQL for durability and audit
	hold := &reservation.Hold{
		ID:             holdID,
		InventoryID:    item.ID,
		CustomerID:     req.CustomerID,
		IdempotencyKey: req.IdempotencyKey,
		Quantity:       req.Quantity,
		Status:         reservation.HoldStatusPending,
		ExpiresAt:      expiresAt,
	}

	if err := s.pgRepo.CreateHold(ctx, hold); err != nil {
		// Compensate in Redis if Postgres write fails
		if s.redisRepo != nil {
			_ = s.redisRepo.ReleaseHold(ctx, holdID, req.SKU)
		}
		return nil, fmt.Errorf("failed to persist hold in database: %w", err)
	}

	return hold, nil
}

// ConfirmHold finalizes the hold after successful customer checkout/payment.
func (s *ReservationService) ConfirmHold(ctx context.Context, holdID uuid.UUID) (*reservation.Hold, error) {
	hold, err := s.pgRepo.GetHoldByID(ctx, holdID)
	if err != nil {
		return nil, err
	}

	if hold.Status == reservation.HoldStatusConfirmed {
		return hold, nil // Already confirmed
	}

	if hold.Status != reservation.HoldStatusPending {
		return nil, fmt.Errorf("%w: status is %s", reservation.ErrHoldAlreadyProcessed, hold.Status)
	}

	if hold.IsExpired() {
		_ = s.pgRepo.UpdateHoldStatus(ctx, holdID, reservation.HoldStatusExpired)
		return nil, ErrHoldExpiredAlready
	}

	// 1. Confirm in Redis
	if s.redisRepo != nil {
		if err := s.redisRepo.ConfirmHold(ctx, holdID); err != nil && !errors.Is(err, redispkg.ErrHoldNotFound) {
			return nil, err
		}
	}

	// 2. Confirm in PostgreSQL and update physical total stock
	if err := s.pgRepo.UpdateHoldStatus(ctx, holdID, reservation.HoldStatusConfirmed); err != nil {
		return nil, err
	}

	if err := s.pgRepo.UpdateStockOnConfirmation(ctx, hold.InventoryID, hold.Quantity); err != nil {
		return nil, err
	}

	hold.Status = reservation.HoldStatusConfirmed
	return hold, nil
}

// ReleaseHold cancels a pending hold and restores inventory stock immediately.
func (s *ReservationService) ReleaseHold(ctx context.Context, holdID uuid.UUID) error {
	hold, err := s.pgRepo.GetHoldByID(ctx, holdID)
	if err != nil {
		return err
	}

	if hold.Status == reservation.HoldStatusReleased {
		return nil
	}

	item, err := s.pgRepo.GetItemByID(ctx, hold.InventoryID)
	if err != nil {
		return err
	}

	// 1. Release in Redis (reclaim stock)
	if s.redisRepo != nil {
		_ = s.redisRepo.ReleaseHold(ctx, holdID, item.SKU)
	}

	// 2. Mark as RELEASED in PostgreSQL
	return s.pgRepo.UpdateHoldStatus(ctx, holdID, reservation.HoldStatusReleased)
}
