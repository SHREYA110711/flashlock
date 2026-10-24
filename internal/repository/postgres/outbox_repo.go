package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type OutboxStatus string

const (
	OutboxStatusPending    OutboxStatus = "PENDING"
	OutboxStatusProcessing OutboxStatus = "PROCESSING"
	OutboxStatusPublished  OutboxStatus = "PUBLISHED"
	OutboxStatusFailed     OutboxStatus = "FAILED"
)

type OutboxEvent struct {
	ID            uuid.UUID              `json:"id"`
	AggregateType string                 `json:"aggregate_type"`
	AggregateID   uuid.UUID              `json:"aggregate_id"`
	EventType     string                 `json:"event_type"`
	Payload       map[string]interface{} `json:"payload"`
	Status        OutboxStatus           `json:"status"`
	RetryCount    int                    `json:"retry_count"`
	LastError     string                 `json:"last_error,omitempty"`
	CreatedAt     time.Time              `json:"created_at"`
	ProcessedAt   *time.Time             `json:"processed_at,omitempty"`
}

type WebhookSubscription struct {
	ID         uuid.UUID `json:"id"`
	URL        string    `json:"url"`
	EventTypes []string  `json:"event_types"`
	IsActive   bool      `json:"is_active"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type OutboxRepository struct {
	pool *pgxpool.Pool
}

func NewOutboxRepository(pool *pgxpool.Pool) *OutboxRepository {
	return &OutboxRepository{pool: pool}
}

// RecordEventTx writes an event within an ongoing database transaction.
// Guarantees atomic dual-write: event is persisted if and only if business data commits.
func (r *OutboxRepository) RecordEventTx(
	ctx context.Context,
	tx pgx.Tx,
	aggregateType string,
	aggregateID uuid.UUID,
	eventType string,
	payload map[string]interface{},
) (*OutboxEvent, error) {
	event := &OutboxEvent{
		ID:            uuid.New(),
		AggregateType: aggregateType,
		AggregateID:   aggregateID,
		EventType:     eventType,
		Payload:       payload,
		Status:        OutboxStatusPending,
		RetryCount:    0,
		CreatedAt:     time.Now().UTC(),
	}

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal outbox event payload: %w", err)
	}

	query := `
		INSERT INTO outbox_events (id, aggregate_type, aggregate_id, event_type, payload, status, retry_count, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`
	_, err = tx.Exec(ctx, query,
		event.ID, event.AggregateType, event.AggregateID, event.EventType,
		payloadJSON, string(event.Status), event.RetryCount, event.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to insert outbox event: %w", err)
	}

	return event, nil
}

// GetPendingEvents retrieves pending events with FOR UPDATE SKIP LOCKED to support multi-worker concurrency.
func (r *OutboxRepository) GetPendingEvents(ctx context.Context, batchSize int) ([]OutboxEvent, error) {
	if batchSize <= 0 {
		batchSize = 50
	}

	query := `
		SELECT id, aggregate_type, aggregate_id, event_type, payload, status, retry_count, last_error, created_at, processed_at
		FROM outbox_events
		WHERE status = 'PENDING'
		ORDER BY created_at ASC
		LIMIT $1
		FOR UPDATE SKIP LOCKED
	`
	rows, err := r.pool.Query(ctx, query, batchSize)
	if err != nil {
		return nil, fmt.Errorf("failed to query pending outbox events: %w", err)
	}
	defer rows.Close()

	var events []OutboxEvent
	for rows.Next() {
		var e OutboxEvent
		var status string
		var payloadJSON []byte
		var lastError *string
		var processedAt *time.Time

		if err := rows.Scan(
			&e.ID, &e.AggregateType, &e.AggregateID, &e.EventType,
			&payloadJSON, &status, &e.RetryCount, &lastError, &e.CreatedAt, &processedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan outbox event: %w", err)
		}

		e.Status = OutboxStatus(status)
		if lastError != nil {
			e.LastError = *lastError
		}
		e.ProcessedAt = processedAt
		_ = json.Unmarshal(payloadJSON, &e.Payload)
		events = append(events, e)
	}

	return events, nil
}

// MarkEventPublished marks an event as successfully delivered.
func (r *OutboxRepository) MarkEventPublished(ctx context.Context, id uuid.UUID) error {
	now := time.Now().UTC()
	query := `
		UPDATE outbox_events
		SET status = 'PUBLISHED', processed_at = $1
		WHERE id = $2
	`
	_, err := r.pool.Exec(ctx, query, now, id)
	return err
}

// MarkEventFailed updates the retry count and records the failure error.
func (r *OutboxRepository) MarkEventFailed(ctx context.Context, id uuid.UUID, errMsg string) error {
	query := `
		UPDATE outbox_events
		SET retry_count = retry_count + 1,
		    last_error = $1,
		    status = CASE WHEN retry_count >= 5 THEN 'FAILED' ELSE 'PENDING' END
		WHERE id = $2
	`
	_, err := r.pool.Exec(ctx, query, errMsg, id)
	return err
}

// RegisterWebhook persists an active webhook subscriber.
func (r *OutboxRepository) RegisterWebhook(ctx context.Context, sub *WebhookSubscription) error {
	if sub.ID == uuid.Nil {
		sub.ID = uuid.New()
	}
	now := time.Now().UTC()
	sub.CreatedAt = now
	sub.UpdatedAt = now
	sub.IsActive = true

	query := `
		INSERT INTO webhook_subscriptions (id, url, event_types, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`
	_, err := r.pool.Exec(ctx, query, sub.ID, sub.URL, sub.EventTypes, sub.IsActive, sub.CreatedAt, sub.UpdatedAt)
	return err
}

// GetActiveWebhooks retrieves all currently subscribed webhooks.
func (r *OutboxRepository) GetActiveWebhooks(ctx context.Context) ([]WebhookSubscription, error) {
	query := `
		SELECT id, url, event_types, is_active, created_at, updated_at
		FROM webhook_subscriptions
		WHERE is_active = TRUE
	`
	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subs []WebhookSubscription
	for rows.Next() {
		var s WebhookSubscription
		if err := rows.Scan(&s.ID, &s.URL, &s.EventTypes, &s.IsActive, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		subs = append(subs, s)
	}
	return subs, nil
}
