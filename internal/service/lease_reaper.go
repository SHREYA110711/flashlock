package service

import (
	"context"
	"log"
	"time"

	"flashlock/internal/repository/postgres"
)

// LeaseReaper periodically sweeps expired PENDING holds and releases stock back to the active pool.
type LeaseReaper struct {
	invRepo            *postgres.InventoryRepository
	reservationService *ReservationService
	interval           time.Duration
	batchSize          int
}

func NewLeaseReaper(
	invRepo *postgres.InventoryRepository,
	reservationService *ReservationService,
	interval time.Duration,
	batchSize int,
) *LeaseReaper {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	if batchSize <= 0 {
		batchSize = 100
	}
	return &LeaseReaper{
		invRepo:            invRepo,
		reservationService: reservationService,
		interval:           interval,
		batchSize:          batchSize,
	}
}

// Start runs the sweep loop until the context is canceled.
func (r *LeaseReaper) Start(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	log.Printf("🧹 Lease Reaper started (interval: %v, batch: %d)", r.interval, r.batchSize)

	for {
		select {
		case <-ctx.Done():
			log.Println("🛑 Lease Reaper shutting down...")
			return
		case <-ticker.C:
			r.reapExpiredHolds(ctx)
		}
	}
}

func (r *LeaseReaper) reapExpiredHolds(ctx context.Context) {
	sweepCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	expiredHolds, err := r.invRepo.GetExpiredPendingHolds(sweepCtx, r.batchSize)
	if err != nil {
		// Log error unless context was canceled during shutdown
		if !errorsIsDone(err, ctx) {
			log.Printf("⚠️  Lease Reaper query error: %v", err)
		}
		return
	}

	if len(expiredHolds) == 0 {
		return
	}

	reapedCount := 0
	for _, hold := range expiredHolds {
		if err := r.reservationService.ReleaseHold(sweepCtx, hold.ID); err != nil {
			log.Printf("⚠️  Failed to release expired hold %s: %v", hold.ID, err)
		} else {
			reapedCount++
		}
	}

	if reapedCount > 0 {
		log.Printf("♻️  Lease Reaper reclaimed %d expired hold(s) back to inventory pool", reapedCount)
	}
}

func errorsIsDone(err error, ctx context.Context) bool {
	return ctx.Err() != nil
}
