-- FlashLock Database Seed Script
-- Creates demo accounts and hot-spot inventory items for testing

-- 1. Insert Demo Ledger Accounts
-- Merchant Operating Cash (Asset)
INSERT INTO accounts (id, name, type, currency, balance, version, created_at, updated_at)
VALUES (
    '11111111-1111-1111-1111-111111111111',
    'Merchant Operating Cash',
    'ASSET',
    'USD',
    0.0000,
    1,
    NOW(),
    NOW()
) ON CONFLICT (id) DO NOTHING;

-- Merchant Sales Revenue (Revenue)
INSERT INTO accounts (id, name, type, currency, balance, version, created_at, updated_at)
VALUES (
    '22222222-2222-2222-2222-222222222222',
    'Merchant Sales Revenue',
    'REVENUE',
    'USD',
    0.0000,
    1,
    NOW(),
    NOW()
) ON CONFLICT (id) DO NOTHING;

-- Customer Demo Wallet (Asset with initial funds)
INSERT INTO accounts (id, name, type, currency, balance, version, created_at, updated_at)
VALUES (
    '33333333-3333-3333-3333-333333333333',
    'Customer Alice Wallet',
    'ASSET',
    'USD',
    5000.0000,
    1,
    NOW(),
    NOW()
) ON CONFLICT (id) DO NOTHING;

-- Customer Bob Wallet (Asset with initial funds)
INSERT INTO accounts (id, name, type, currency, balance, version, created_at, updated_at)
VALUES (
    '44444444-4444-4444-4444-444444444444',
    'Customer Bob Wallet',
    'ASSET',
    'USD',
    2500.0000,
    1,
    NOW(),
    NOW()
) ON CONFLICT (id) DO NOTHING;


-- 2. Insert Flash-Sale Inventory Items (High Contention Hot-Spots)
-- Flash-Sale Concert VIP Tickets (Only 10 tickets for thousands of fans)
INSERT INTO inventory_items (id, sku, name, total_stock, reserved_stock, price, currency, version, created_at, updated_at)
VALUES (
    '55555555-5555-5555-5555-555555555555',
    'VIP-CONCERT-TICKET-2026',
    'Coldplay VIP Front Row Ticket',
    10,
    0,
    299.9900,
    'USD',
    1,
    NOW(),
    NOW()
) ON CONFLICT (sku) DO NOTHING;

-- Flash-Sale Limited Sneaker Drop (Only 25 pairs available)
INSERT INTO inventory_items (id, sku, name, total_stock, reserved_stock, price, currency, version, created_at, updated_at)
VALUES (
    '66666666-6666-6666-6666-666666666666',
    'AIR-JORDAN-RETRO-LIMITED',
    'Air Jordan 1 Retro High OG Limited Edition',
    25,
    0,
    190.0000,
    'USD',
    1,
    NOW(),
    NOW()
) ON CONFLICT (sku) DO NOTHING;
