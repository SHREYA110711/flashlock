-- Down Migration: Drop tables and triggers

DROP TRIGGER IF EXISTS trg_prevent_entry_modification ON entries;
DROP FUNCTION IF EXISTS prevent_entry_modification();

DROP TABLE IF EXISTS reservation_holds;
DROP TABLE IF EXISTS inventory_items;
DROP TABLE IF EXISTS entries;
DROP TABLE IF EXISTS transactions;
DROP TABLE IF EXISTS accounts;
