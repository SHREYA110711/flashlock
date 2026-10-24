package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"flashlock/internal/repository/postgres"
)

type OutboxWorker struct {
	outboxRepo *postgres.OutboxRepository
	httpClient *http.Client
	interval   time.Duration
	batchSize  int
}

func NewOutboxWorker(
	outboxRepo *postgres.OutboxRepository,
	interval time.Duration,
	batchSize int,
) *OutboxWorker {
	if interval <= 0 {
		interval = 1 * time.Second
	}
	if batchSize <= 0 {
		batchSize = 50
	}
	return &OutboxWorker{
		outboxRepo: outboxRepo,
		httpClient: &http.Client{Timeout: 5 * time.Second},
		interval:   interval,
		batchSize:  batchSize,
	}
}

// Start begins the asynchronous polling and dispatching loop.
func (w *OutboxWorker) Start(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	log.Printf("📦 Outbox Event Worker started (interval: %v, batch: %d)", w.interval, w.batchSize)

	for {
		select {
		case <-ctx.Done():
			log.Println("🛑 Outbox Event Worker shutting down...")
			return
		case <-ticker.C:
			w.processOutboxBatch(ctx)
		}
	}
}

func (w *OutboxWorker) processOutboxBatch(ctx context.Context) {
	pollCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	events, err := w.outboxRepo.GetPendingEvents(pollCtx, w.batchSize)
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("⚠️  Outbox worker query error: %v", err)
		}
		return
	}

	if len(events) == 0 {
		return
	}

	// Fetch active subscribers
	webhooks, _ := w.outboxRepo.GetActiveWebhooks(pollCtx)

	for _, event := range events {
		dispatchErr := w.dispatchEvent(pollCtx, event, webhooks)
		if dispatchErr != nil {
			log.Printf("⚠️  Failed to dispatch outbox event %s (%s): %v", event.ID, event.EventType, dispatchErr)
			_ = w.outboxRepo.MarkEventFailed(pollCtx, event.ID, dispatchErr.Error())
		} else {
			_ = w.outboxRepo.MarkEventPublished(pollCtx, event.ID)
		}
	}
}

func (w *OutboxWorker) dispatchEvent(ctx context.Context, event postgres.OutboxEvent, webhooks []postgres.WebhookSubscription) error {
	// If no webhooks are registered, mark as published (consumed internally)
	if len(webhooks) == 0 {
		return nil
	}

	payloadBytes, err := json.Marshal(map[string]interface{}{
		"id":             event.ID,
		"event_type":     event.EventType,
		"aggregate_type": event.AggregateType,
		"aggregate_id":   event.AggregateID,
		"payload":        event.Payload,
		"timestamp":      event.CreatedAt.Format(time.RFC3339),
	})
	if err != nil {
		return fmt.Errorf("failed to marshal event dispatch payload: %w", err)
	}

	for _, sub := range webhooks {
		// Check if subscription matches event type (or wildcard "*")
		matches := false
		for _, t := range sub.EventTypes {
			if t == "*" || t == event.EventType {
				matches = true
				break
			}
		}

		if !matches {
			continue
		}

		req, err := http.NewRequestWithContext(ctx, "POST", sub.URL, bytes.NewReader(payloadBytes))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-FlashLock-Event", event.EventType)

		resp, err := w.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("webhook endpoint %s failed: %w", sub.URL, err)
		}
		_ = resp.Body.Close()

		if resp.StatusCode >= 400 {
			return fmt.Errorf("webhook endpoint %s returned status %d", sub.URL, resp.StatusCode)
		}
	}

	return nil
}
