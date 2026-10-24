package redis

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"flashlock/internal/domain/reservation"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var (
	ErrInsufficientStock = errors.New("insufficient inventory available to reserve")
	ErrHoldNotFound      = errors.New("reservation hold not found or expired")
	ErrHoldProcessed     = errors.New("reservation hold has already been processed")
)

// Lua Script: Atomic Check-and-Decrement Hold
// KEYS[1]: inventory stock key (e.g., inventory:{sku}:stock)
// KEYS[2]: hold key (e.g., hold:{hold_id})
// ARGV[1]: requested quantity
// ARGV[2]: customer_id
// ARGV[3]: sku
// ARGV[4]: TTL in seconds
// ARGV[5]: current unix timestamp
const reserveHoldLuaScript = `
local stock_key = KEYS[1]
local hold_key = KEYS[2]

local qty = tonumber(ARGV[1])
local customer_id = ARGV[2]
local sku = ARGV[3]
local ttl_seconds = tonumber(ARGV[4])
local now = tonumber(ARGV[5])

-- Check if hold key already exists (Idempotency)
if redis.call("EXISTS", hold_key) == 1 then
    local existing_status = redis.call("HGET", hold_key, "status")
    return {1, existing_status} -- Already created
end

-- Check current stock
local current_stock = tonumber(redis.call("GET", stock_key) or "0")
if current_stock < qty then
    return {-1, "INSUFFICIENT_STOCK"}
end

-- Atomically decrement stock
redis.call("DECRBY", stock_key, qty)

-- Store hold record
local expires_at = now + ttl_seconds
redis.call("HSET", hold_key,
    "sku", sku,
    "customer_id", customer_id,
    "quantity", qty,
    "status", "PENDING",
    "expires_at", expires_at
)
redis.call("EXPIRE", hold_key, ttl_seconds)

return {0, tostring(expires_at)}
`

// Lua Script: Atomic Release Hold (Reclaim stock back to pool)
// KEYS[1]: inventory stock key (e.g., inventory:{sku}:stock)
// KEYS[2]: hold key (e.g., hold:{hold_id})
const releaseHoldLuaScript = `
local stock_key = KEYS[1]
local hold_key = KEYS[2]

if redis.call("EXISTS", hold_key) == 0 then
    return {-1, "HOLD_NOT_FOUND"}
end

local status = redis.call("HGET", hold_key, "status")
if status ~= "PENDING" then
    return {-2, status}
end

local qty = tonumber(redis.call("HGET", hold_key, "quantity") or "0")

-- Reclaim stock back to pool
redis.call("INCRBY", stock_key, qty)
redis.call("HSET", hold_key, "status", "RELEASED")
redis.call("DEL", hold_key)

return {0, "RELEASED"}
`

// Lua Script: Atomic Confirm Hold (Stock remains decremented)
// KEYS[1]: hold key (e.g., hold:{hold_id})
const confirmHoldLuaScript = `
local hold_key = KEYS[1]

if redis.call("EXISTS", hold_key) == 0 then
    return {-1, "HOLD_NOT_FOUND"}
end

local status = redis.call("HGET", hold_key, "status")
if status ~= "PENDING" then
    return {-2, status}
end

redis.call("HSET", hold_key, "status", "CONFIRMED")
-- Persist or keep for confirmation window
redis.call("PERSIST", hold_key)

return {0, "CONFIRMED"}
`

type ReservationRedisRepository struct {
	client           *redis.Client
	reserveScript    *redis.Script
	releaseScript    *redis.Script
	confirmScript    *redis.Script
}

func NewReservationRedisRepository(client *redis.Client) *ReservationRedisRepository {
	return &ReservationRedisRepository{
		client:        client,
		reserveScript: redis.NewScript(reserveHoldLuaScript),
		releaseScript: redis.NewScript(releaseHoldLuaScript),
		confirmScript: redis.NewScript(confirmHoldLuaScript),
	}
}

// SetInitialStock sets or updates the atomic stock count in Redis.
func (r *ReservationRedisRepository) SetInitialStock(ctx context.Context, sku string, availableStock int) error {
	stockKey := fmt.Sprintf("inventory:%s:stock", sku)
	return r.client.Set(ctx, stockKey, availableStock, 0).Err()
}

// GetAvailableStock retrieves current in-memory stock.
func (r *ReservationRedisRepository) GetAvailableStock(ctx context.Context, sku string) (int, error) {
	stockKey := fmt.Sprintf("inventory:%s:stock", sku)
	val, err := r.client.Get(ctx, stockKey).Int()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return 0, nil
		}
		return 0, err
	}
	return val, nil
}

// ReserveHold attempts atomic hold allocation with TTL lease.
func (r *ReservationRedisRepository) ReserveHold(
	ctx context.Context,
	holdID uuid.UUID,
	sku string,
	customerID string,
	quantity int,
	ttlSeconds int,
) (time.Time, error) {
	stockKey := fmt.Sprintf("inventory:%s:stock", sku)
	holdKey := fmt.Sprintf("hold:%s", holdID.String())
	now := time.Now().UTC().Unix()

	keys := []string{stockKey, holdKey}
	args := []interface{}{quantity, customerID, sku, ttlSeconds, now}

	res, err := r.reserveScript.Run(ctx, r.client, keys, args...).Slice()
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to execute reserve lua script: %w", err)
	}

	code := res[0].(int64)
	val := res[1].(string)

	if code == -1 {
		return time.Time{}, ErrInsufficientStock
	}

	if code == 1 {
		// Already exists (Idempotent hold creation)
		expiresUnix, _ := strconv.ParseInt(val, 10, 64)
		return time.Unix(expiresUnix, 0).UTC(), nil
	}

	expiresUnix, err := strconv.ParseInt(val, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid expiration timestamp from lua: %w", err)
	}

	return time.Unix(expiresUnix, 0).UTC(), nil
}

// ReleaseHold releases reserved stock back to the Redis pool.
func (r *ReservationRedisRepository) ReleaseHold(ctx context.Context, holdID uuid.UUID, sku string) error {
	stockKey := fmt.Sprintf("inventory:%s:stock", sku)
	holdKey := fmt.Sprintf("hold:%s", holdID.String())

	res, err := r.releaseScript.Run(ctx, r.client, []string{stockKey, holdKey}).Slice()
	if err != nil {
		return fmt.Errorf("failed to execute release lua script: %w", err)
	}

	code := res[0].(int64)
	if code == -1 {
		return ErrHoldNotFound
	}
	if code == -2 {
		return fmt.Errorf("%w: current status=%s", ErrHoldProcessed, res[1].(string))
	}

	return nil
}

// ConfirmHold finalizes the hold in Redis.
func (r *ReservationRedisRepository) ConfirmHold(ctx context.Context, holdID uuid.UUID) error {
	holdKey := fmt.Sprintf("hold:%s", holdID.String())

	res, err := r.confirmScript.Run(ctx, r.client, []string{holdKey}).Slice()
	if err != nil {
		return fmt.Errorf("failed to execute confirm lua script: %w", err)
	}

	code := res[0].(int64)
	if code == -1 {
		return ErrHoldNotFound
	}
	if code == -2 {
		return fmt.Errorf("%w: current status=%s", ErrHoldProcessed, res[1].(string))
	}

	return nil
}

// GetHoldStatus inspects hold details from Redis.
func (r *ReservationRedisRepository) GetHoldStatus(ctx context.Context, holdID uuid.UUID) (*reservation.Hold, error) {
	holdKey := fmt.Sprintf("hold:%s", holdID.String())
	vals, err := r.client.HGetAll(ctx, holdKey).Result()
	if err != nil {
		return nil, err
	}
	if len(vals) == 0 {
		return nil, ErrHoldNotFound
	}

	qty, _ := strconv.Atoi(vals["quantity"])
	expiresUnix, _ := strconv.ParseInt(vals["expires_at"], 10, 64)

	return &reservation.Hold{
		ID:         holdID,
		CustomerID: vals["customer_id"],
		Quantity:   qty,
		Status:     reservation.HoldStatus(vals["status"]),
		ExpiresAt:  time.Unix(expiresUnix, 0).UTC(),
	}, nil
}
