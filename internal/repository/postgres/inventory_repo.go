package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"flashlock/internal/domain/reservation"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrInventoryNotFound = errors.New("inventory item not found")
	ErrHoldNotFoundInDB  = errors.New("reservation hold not found in database")
)

type InventoryRepository struct {
	pool *pgxpool.Pool
}

func NewInventoryRepository(pool *pgxpool.Pool) *InventoryRepository {
	return &InventoryRepository{pool: pool}
}

// CreateItem persists a new inventory SKU.
func (r *InventoryRepository) CreateItem(ctx context.Context, item *reservation.InventoryItem) error {
	if item.ID == uuid.Nil {
		item.ID = uuid.New()
	}
	now := time.Now().UTC()
	item.CreatedAt = now
	item.UpdatedAt = now
	item.Version = 1

	query := `
		INSERT INTO inventory_items (id, sku, name, total_stock, reserved_stock, price, currency, version, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`
	_, err := r.pool.Exec(ctx, query,
		item.ID, item.SKU, item.Name, item.TotalStock, item.ReservedStock,
		item.Price, item.Currency, item.Version, item.CreatedAt, item.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to insert inventory item: %w", err)
	}
	return nil
}

// GetItemBySKU retrieves an item by SKU.
func (r *InventoryRepository) GetItemBySKU(ctx context.Context, sku string) (*reservation.InventoryItem, error) {
	query := `
		SELECT id, sku, name, total_stock, reserved_stock, price, currency, version, created_at, updated_at
		FROM inventory_items
		WHERE sku = $1
	`
	var item reservation.InventoryItem
	err := r.pool.QueryRow(ctx, query, sku).Scan(
		&item.ID, &item.SKU, &item.Name, &item.TotalStock, &item.ReservedStock,
		&item.Price, &item.Currency, &item.Version, &item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrInventoryNotFound
		}
		return nil, fmt.Errorf("failed to query inventory item by sku: %w", err)
	}
	return &item, nil
}

// GetItemByID retrieves an item by ID.
func (r *InventoryRepository) GetItemByID(ctx context.Context, id uuid.UUID) (*reservation.InventoryItem, error) {
	query := `
		SELECT id, sku, name, total_stock, reserved_stock, price, currency, version, created_at, updated_at
		FROM inventory_items
		WHERE id = $1
	`
	var item reservation.InventoryItem
	err := r.pool.QueryRow(ctx, query, id).Scan(
		&item.ID, &item.SKU, &item.Name, &item.TotalStock, &item.ReservedStock,
		&item.Price, &item.Currency, &item.Version, &item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrInventoryNotFound
		}
		return nil, fmt.Errorf("failed to query inventory item by id: %w", err)
	}
	return &item, nil
}

// CreateHold persists a leased reservation record in PostgreSQL.
func (r *InventoryRepository) CreateHold(ctx context.Context, hold *reservation.Hold) error {
	now := time.Now().UTC()
	hold.CreatedAt = now
	hold.UpdatedAt = now

	query := `
		INSERT INTO reservation_holds (id, inventory_id, customer_id, idempotency_key, quantity, status, expires_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`
	_, err := r.pool.Exec(ctx, query,
		hold.ID, hold.InventoryID, hold.CustomerID, hold.IdempotencyKey,
		hold.Quantity, string(hold.Status), hold.ExpiresAt, hold.CreatedAt, hold.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to insert reservation hold: %w", err)
	}
	return nil
}

// GetHoldByID retrieves a hold record.
func (r *InventoryRepository) GetHoldByID(ctx context.Context, id uuid.UUID) (*reservation.Hold, error) {
	query := `
		SELECT id, inventory_id, customer_id, idempotency_key, quantity, status, expires_at, created_at, updated_at
		FROM reservation_holds
		WHERE id = $1
	`
	var hold reservation.Hold
	var status string
	err := r.pool.QueryRow(ctx, query, id).Scan(
		&hold.ID, &hold.InventoryID, &hold.CustomerID, &hold.IdempotencyKey,
		&hold.Quantity, &status, &hold.ExpiresAt, &hold.CreatedAt, &hold.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrHoldNotFoundInDB
		}
		return nil, fmt.Errorf("failed to query hold: %w", err)
	}
	hold.Status = reservation.HoldStatus(status)
	return &hold, nil
}

// GetHoldByIdempotencyKey checks if a hold was already requested with this idempotency key.
func (r *InventoryRepository) GetHoldByIdempotencyKey(ctx context.Context, key string) (*reservation.Hold, error) {
	query := `
		SELECT id, inventory_id, customer_id, idempotency_key, quantity, status, expires_at, created_at, updated_at
		FROM reservation_holds
		WHERE idempotency_key = $1
	`
	var hold reservation.Hold
	var status string
	err := r.pool.QueryRow(ctx, query, key).Scan(
		&hold.ID, &hold.InventoryID, &hold.CustomerID, &hold.IdempotencyKey,
		&hold.Quantity, &status, &hold.ExpiresAt, &hold.CreatedAt, &hold.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrHoldNotFoundInDB
		}
		return nil, fmt.Errorf("failed to query hold by idempotency key: %w", err)
	}
	hold.Status = reservation.HoldStatus(status)
	return &hold, nil
}

// UpdateHoldStatus updates status in PostgreSQL.
func (r *InventoryRepository) UpdateHoldStatus(ctx context.Context, holdID uuid.UUID, status reservation.HoldStatus) error {
	query := `
		UPDATE reservation_holds
		SET status = $1, updated_at = $2
		WHERE id = $3
	`
	res, err := r.pool.Exec(ctx, query, string(status), time.Now().UTC(), holdID)
	if err != nil {
		return fmt.Errorf("failed to update hold status: %w", err)
	}
	if res.RowsAffected() == 0 {
		return ErrHoldNotFoundInDB
	}
	return nil
}

// UpdateStockOnConfirmation decrements total stock in PostgreSQL when hold is settled.
func (r *InventoryRepository) UpdateStockOnConfirmation(ctx context.Context, inventoryID uuid.UUID, quantity int) error {
	query := `
		UPDATE inventory_items
		SET total_stock = total_stock - $1, version = version + 1, updated_at = $2
		WHERE id = $3 AND total_stock >= $1
	`
	res, err := r.pool.Exec(ctx, query, quantity, time.Now().UTC(), inventoryID)
	if err != nil {
		return fmt.Errorf("failed to decrement inventory stock: %w", err)
	}
	if res.RowsAffected() == 0 {
		return errors.New("insufficient inventory or item not found during settlement")
	}
	return nil
}

// GetExpiredPendingHolds finds holds that passed their TTL without being confirmed.
func (r *InventoryRepository) GetExpiredPendingHolds(ctx context.Context, limit int) ([]reservation.Hold, error) {
	query := `
		SELECT id, inventory_id, customer_id, idempotency_key, quantity, status, expires_at, created_at, updated_at
		FROM reservation_holds
		WHERE status = 'PENDING' AND expires_at < $1
		ORDER BY expires_at ASC
		LIMIT $2
	`
	rows, err := r.pool.Query(ctx, query, time.Now().UTC(), limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query expired holds: %w", err)
	}
	defer rows.Close()

	var holds []reservation.Hold
	for rows.Next() {
		var h reservation.Hold
		var status string
		if err := rows.Scan(
			&h.ID, &h.InventoryID, &h.CustomerID, &h.IdempotencyKey,
			&h.Quantity, &status, &h.ExpiresAt, &h.CreatedAt, &h.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan expired hold: %w", err)
		}
		h.Status = reservation.HoldStatus(status)
		holds = append(holds, h)
	}
	return holds, nil
}

// GetHoldsBySKU retrieves holds associated with an inventory SKU.
func (r *InventoryRepository) GetHoldsBySKU(ctx context.Context, sku string, limit, offset int) ([]reservation.Hold, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	query := `
		SELECT h.id, h.inventory_id, h.customer_id, h.idempotency_key, h.quantity, h.status, h.expires_at, h.created_at, h.updated_at
		FROM reservation_holds h
		JOIN inventory_items i ON h.inventory_id = i.id
		WHERE i.sku = $1
		ORDER BY h.created_at DESC
		LIMIT $2 OFFSET $3
	`
	rows, err := r.pool.Query(ctx, query, sku, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query holds by sku: %w", err)
	}
	defer rows.Close()

	var holds []reservation.Hold
	for rows.Next() {
		var h reservation.Hold
		var status string
		if err := rows.Scan(
			&h.ID, &h.InventoryID, &h.CustomerID, &h.IdempotencyKey,
			&h.Quantity, &status, &h.ExpiresAt, &h.CreatedAt, &h.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan hold: %w", err)
		}
		h.Status = reservation.HoldStatus(status)
		holds = append(holds, h)
	}
	return holds, nil
}


