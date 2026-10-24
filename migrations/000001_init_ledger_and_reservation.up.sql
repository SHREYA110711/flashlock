-- Up Migration: Double-Entry Ledger & High-Concurrency Reservation Schema

-- Enable UUID extension
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- ============================================================================
-- 1. DOUBLE-ENTRY LEDGER ENGINE
-- ============================================================================

-- Account Types:
-- ASSET: Normal Balance = Debit (Cash, Inventory, Receivables)
-- LIABILITY: Normal Balance = Credit (Customer deposits, Accounts Payable, Unearned Revenue)
-- EQUITY: Normal Balance = Credit (Retained earnings, Capital)
-- REVENUE: Normal Balance = Credit (Sales, Fee income)
-- EXPENSE: Normal Balance = Debit (Operating cost, Processing fees)
CREATE TABLE IF NOT EXISTS accounts (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name VARCHAR(255) NOT NULL,
    type VARCHAR(32) NOT NULL CHECK (type IN ('ASSET', 'LIABILITY', 'EQUITY', 'REVENUE', 'EXPENSE')),
    currency VARCHAR(3) NOT NULL DEFAULT 'USD',
    balance NUMERIC(20, 4) NOT NULL DEFAULT 0.0000,
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_accounts_type ON accounts(type);
CREATE INDEX IF NOT EXISTS idx_accounts_currency ON accounts(currency);

-- Transactions: Business events containing multi-legged debit & credit entries
CREATE TABLE IF NOT EXISTS transactions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    idempotency_key VARCHAR(255) UNIQUE NOT NULL,
    description TEXT NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'POSTED' CHECK (status IN ('POSTED', 'VOIDED')),
    metadata JSONB DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_transactions_idempotency ON transactions(idempotency_key);
CREATE INDEX IF NOT EXISTS idx_transactions_created_at ON transactions(created_at);

-- Entries: Append-only ledger line items (strict double-entry components)
CREATE TABLE IF NOT EXISTS entries (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    transaction_id UUID NOT NULL REFERENCES transactions(id) ON DELETE RESTRICT,
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    direction VARCHAR(6) NOT NULL CHECK (direction IN ('DEBIT', 'CREDIT')),
    amount NUMERIC(20, 4) NOT NULL CHECK (amount > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_entries_transaction_id ON entries(transaction_id);
CREATE INDEX IF NOT EXISTS idx_entries_account_id ON entries(account_id);
CREATE INDEX IF NOT EXISTS idx_entries_created_at ON entries(created_at);

-- Append-Only enforcement rule: Prevent UPDATE or DELETE on historical entries
CREATE OR REPLACE FUNCTION prevent_entry_modification()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'Double-entry ledger entries are immutable. UPDATE or DELETE is strictly forbidden.';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_prevent_entry_modification ON entries;
CREATE TRIGGER trg_prevent_entry_modification
BEFORE UPDATE OR DELETE ON entries
FOR EACH ROW EXECUTE FUNCTION prevent_entry_modification();


-- ============================================================================
-- 2. HIGH-CONCURRENCY RESERVATION ENGINE
-- ============================================================================

-- Inventory Items: Base stock definition
CREATE TABLE IF NOT EXISTS inventory_items (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    sku VARCHAR(100) UNIQUE NOT NULL,
    name VARCHAR(255) NOT NULL,
    total_stock INT NOT NULL CHECK (total_stock >= 0),
    reserved_stock INT NOT NULL DEFAULT 0 CHECK (reserved_stock >= 0),
    price NUMERIC(20, 4) NOT NULL CHECK (price >= 0),
    currency VARCHAR(3) NOT NULL DEFAULT 'USD',
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_stock_bounds CHECK (reserved_stock <= total_stock)
);

CREATE INDEX IF NOT EXISTS idx_inventory_sku ON inventory_items(sku);

-- Reservation Holds: Leased stock units held temporarily for checkout
CREATE TABLE IF NOT EXISTS reservation_holds (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    inventory_id UUID NOT NULL REFERENCES inventory_items(id) ON DELETE RESTRICT,
    customer_id VARCHAR(255) NOT NULL,
    idempotency_key VARCHAR(255) UNIQUE NOT NULL,
    quantity INT NOT NULL CHECK (quantity > 0),
    status VARCHAR(32) NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'CONFIRMED', 'EXPIRED', 'RELEASED')),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_holds_inventory_status ON reservation_holds(inventory_id, status);
CREATE INDEX IF NOT EXISTS idx_holds_expires_at ON reservation_holds(expires_at) WHERE status = 'PENDING';
CREATE INDEX IF NOT EXISTS idx_holds_idempotency ON reservation_holds(idempotency_key);
