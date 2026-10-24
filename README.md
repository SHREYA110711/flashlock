# FlashLock ⚡

> High-concurrency distributed reservation engine and immutable double-entry ledger built with Go, PostgreSQL, and Redis.

---

## 🏛️ System Overview

FlashLock bridges two traditionally conflicting domain requirements:
1. **High-Concurrency Reservation Engine**: Sub-millisecond atomic inventory decrement and leased locks with TTL expiration to prevent overselling on hot items under heavy contention.
2. **Double-Entry Ledger Engine**: An append-only, mathematically balanced ($\sum \text{Debits} = \sum \text{Credits}$) financial journal with idempotency enforcement and database-level immutability triggers.

---

## 🚀 Tech Stack

- **Language:** Go 1.23
- **Web Framework:** [Echo v4](https://echo.labstack.com/)
- **Primary Database:** PostgreSQL 16 (with strict check constraints & immutability triggers)
- **Fast Lock / Cache:** Redis 7 (atomic Lua scripts & lease management)
- **Math Precision:** Arbitrary-precision decimal arithmetic (`github.com/shopspring/decimal`)
- **Containerization:** Docker Compose

---

## 🛠️ Project Structure

```text
flashlock/
├── cmd/
│   └── server/
│       └── main.go               # Application entry point & lifecycle
├── internal/
│   ├── config/                   # Strongly-typed environment configuration
│   ├── domain/
│   │   ├── ledger/               # Double-entry models & zero-sum invariants
│   │   └── reservation/          # Inventory & leased hold state machine
│   ├── repository/
│   │   ├── postgres/             # PostgreSQL connection pool & queries
│   │   └── redis/                # Redis client & atomic script execution
│   └── transport/
│       └── http/                 # Echo HTTP server, routes & handlers
├── migrations/                   # DDL schema migrations with SQL constraints
├── docker-compose.yml            # Local PostgreSQL and Redis setup
└── README.md
```

---

## ⚖️ Core Invariants

### Double-Entry Accounting Invariant
Every transaction must consist of two or more entries satisfying:
$$\sum \text{Debits} - \sum \text{Credits} = 0$$

- **Assets / Expenses:** Balance increased by Debits, decreased by Credits.
- **Liabilities / Equity / Revenue:** Balance increased by Credits, decreased by Debits.
- **Append-Only Immutability:** Historical entries cannot be modified or deleted (`prevent_entry_modification` trigger).

### Inventory Reservation Invariant
- Hard database constraint: `CHECK (reserved_stock <= total_stock)`.
- Anti-oversell guarantee enforced through atomic operations.

---

## 🏁 Quickstart

### 1. Prerequisites
- [Go 1.23+](https://go.dev/)
- [Docker & Docker Compose](https://www.docker.com/)

### 2. Start Infrastructure
```bash
docker compose up -d
```

### 3. Run Application
```bash
go run cmd/server/main.go
```

### 4. Health Check
```bash
curl http://localhost:8080/healthz
```

---

## 🗺️ Roadmap

- [x] **Phase 1:** Project Scaffolding, Infrastructure, Echo Web Server & Domain Models
- [x] **Phase 2:** Core Double-Entry Ledger Engine (Atomic Multi-Legged Transactions & Idempotency)
- [x] **Phase 3:** High-Concurrency Reservation Engine (Atomic Redis Lua Holds & TTL Leases)
- [x] **Phase 4:** Two-Phase Saga Orchestrator (Hold -> Settle -> Post)
- [x] **Phase 5:** REST APIs & Ledger Statement Generation
- [ ] **Phase 6:** High-Throughput Load Testing & Race Condition Verification (k6)

---

## 📡 Complete REST API Reference

### 1. High-Concurrency Reservation Engine
| Endpoint | Method | Description |
| :--- | :--- | :--- |
| `/api/v1/inventory` | `POST` | Create inventory item & seed Redis atomic counter |
| `/api/v1/inventory/:sku` | `GET` | Get item details and real-time live stock |
| `/api/v1/inventory/:sku/holds` | `GET` | View active and historical holds for a SKU |
| `/api/v1/reservations/hold` | `POST` | Atomic check-and-decrement hold with TTL lease |
| `/api/v1/reservations/:id/confirm` | `POST` | Confirm hold permanently |
| `/api/v1/reservations/:id/release` | `POST` | Release hold and restore stock immediately |

### 2. Double-Entry Ledger Engine
| Endpoint | Method | Description |
| :--- | :--- | :--- |
| `/api/v1/accounts` | `POST` | Create financial account (`ASSET`, `LIABILITY`, `REVENUE`, etc.) |
| `/api/v1/accounts/:id` | `GET` | Query account details and balance |
| `/api/v1/ledger/transactions` | `POST` | Post multi-legged balanced transaction ($\sum \text{Debit} = \sum \text{Credit}$) |
| `/api/v1/ledger/transactions/:id` | `GET` | Get transaction details and append-only entry legs |
| `/api/v1/ledger/transfers` | `POST` | Execute 2-legged balanced transfer between accounts |

### 3. Auditing & Statements
| Endpoint | Method | Description |
| :--- | :--- | :--- |
| `/api/v1/accounts/:id/statement` | `GET` | Generate chronological statement with running balances |
| `/api/v1/ledger/trial-balance` | `GET` | Audit report verifying $\sum \text{Debits} - \sum \text{Credits} = 0$ globally |

### 4. Two-Phase Saga Checkout
| Endpoint | Method | Description |
| :--- | :--- | :--- |
| `/api/v1/checkout` | `POST` | Atomic 2-phase settlement: Hold Confirm + Ledger Post |
| `/api/v1/checkout/cancel` | `POST` | Cancel checkout and execute compensation |

