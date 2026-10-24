-- Down Migration: Roll back outbox, webhooks, and hash columns

DROP TABLE IF EXISTS webhook_subscriptions;
DROP TABLE IF EXISTS outbox_events;

ALTER TABLE transactions
DROP COLUMN IF EXISTS current_hash,
DROP COLUMN IF EXISTS previous_hash;
