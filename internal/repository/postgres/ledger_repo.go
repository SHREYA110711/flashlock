package postgres

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"flashlock/internal/domain/ledger"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

var (
	ErrAccountNotFound      = errors.New("ledger account not found")
	ErrTransactionNotFound  = errors.New("ledger transaction not found")
	ErrDuplicateIdempotency = errors.New("transaction with this idempotency key already exists")
	ErrInsufficientFunds    = errors.New("insufficient funds for asset account")
)

type LedgerRepository struct {
	pool *pgxpool.Pool
}

func NewLedgerRepository(pool *pgxpool.Pool) *LedgerRepository {
	return &LedgerRepository{pool: pool}
}

// CreateAccount persists a new financial account.
func (r *LedgerRepository) CreateAccount(ctx context.Context, acc *ledger.Account) error {
	if acc.ID == uuid.Nil {
		acc.ID = uuid.New()
	}
	now := time.Now().UTC()
	acc.CreatedAt = now
	acc.UpdatedAt = now
	acc.Version = 1

	query := `
		INSERT INTO accounts (id, name, type, currency, balance, version, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`
	_, err := r.pool.Exec(ctx, query,
		acc.ID, acc.Name, string(acc.Type), acc.Currency, acc.Balance, acc.Version, acc.CreatedAt, acc.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to insert account: %w", err)
	}
	return nil
}

// GetAccountByID retrieves an account by its unique UUID.
func (r *LedgerRepository) GetAccountByID(ctx context.Context, id uuid.UUID) (*ledger.Account, error) {
	query := `
		SELECT id, name, type, currency, balance, version, created_at, updated_at
		FROM accounts
		WHERE id = $1
	`
	var acc ledger.Account
	var accType string

	err := r.pool.QueryRow(ctx, query, id).Scan(
		&acc.ID, &acc.Name, &accType, &acc.Currency, &acc.Balance, &acc.Version, &acc.CreatedAt, &acc.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAccountNotFound
		}
		return nil, fmt.Errorf("failed to query account: %w", err)
	}
	acc.Type = ledger.AccountType(accType)
	return &acc, nil
}

// GetTransactionByID retrieves a transaction and its entries by ID.
func (r *LedgerRepository) GetTransactionByID(ctx context.Context, id uuid.UUID) (*ledger.Transaction, error) {
	query := `
		SELECT id, idempotency_key, description, status, metadata, created_at
		FROM transactions
		WHERE id = $1
	`
	var tx ledger.Transaction
	var status string

	err := r.pool.QueryRow(ctx, query, id).Scan(
		&tx.ID, &tx.IdempotencyKey, &tx.Description, &status, &tx.Metadata, &tx.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTransactionNotFound
		}
		return nil, fmt.Errorf("failed to query transaction: %w", err)
	}
	tx.Status = ledger.TransactionStatus(status)

	entries, err := r.GetEntriesByTransactionID(ctx, tx.ID)
	if err != nil {
		return nil, err
	}
	tx.Entries = entries
	return &tx, nil
}

// GetTransactionByIdempotencyKey checks if a transaction has already been recorded.
func (r *LedgerRepository) GetTransactionByIdempotencyKey(ctx context.Context, key string) (*ledger.Transaction, error) {
	query := `
		SELECT id, idempotency_key, description, status, metadata, created_at
		FROM transactions
		WHERE idempotency_key = $1
	`
	var tx ledger.Transaction
	var status string

	err := r.pool.QueryRow(ctx, query, key).Scan(
		&tx.ID, &tx.IdempotencyKey, &tx.Description, &status, &tx.Metadata, &tx.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTransactionNotFound
		}
		return nil, fmt.Errorf("failed to query transaction by idempotency key: %w", err)
	}
	tx.Status = ledger.TransactionStatus(status)

	// Fetch entries for this transaction
	entries, err := r.GetEntriesByTransactionID(ctx, tx.ID)
	if err != nil {
		return nil, err
	}
	tx.Entries = entries
	return &tx, nil
}

// GetEntriesByTransactionID retrieves all entries belonging to a transaction.
func (r *LedgerRepository) GetEntriesByTransactionID(ctx context.Context, txID uuid.UUID) ([]ledger.Entry, error) {
	query := `
		SELECT id, transaction_id, account_id, direction, amount, created_at
		FROM entries
		WHERE transaction_id = $1
		ORDER BY created_at ASC
	`
	rows, err := r.pool.Query(ctx, query, txID)
	if err != nil {
		return nil, fmt.Errorf("failed to query entries: %w", err)
	}
	defer rows.Close()

	var entries []ledger.Entry
	for rows.Next() {
		var e ledger.Entry
		var dir string
		if err := rows.Scan(&e.ID, &e.TransactionID, &e.AccountID, &dir, &e.Amount, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan entry: %w", err)
		}
		e.Direction = ledger.Direction(dir)
		entries = append(entries, e)
	}
	return entries, nil
}

// PostTransaction executes a multi-legged double-entry transaction atomically.
// Concurrency protections:
// 1. Sorts account IDs deterministically before acquiring row locks (SELECT ... FOR UPDATE) to eliminate deadlocks.
// 2. Mathematical zero-sum validation before persisting.
// 3. Idempotent deduplication: returns existing record if idempotency_key was already processed.
// 4. Invariant: Append-only entries insertion and atomic account balance updating.
func (r *LedgerRepository) PostTransaction(ctx context.Context, tx *ledger.Transaction) (*ledger.Transaction, error) {
	// Step 1: Pre-validation of double-entry mathematical invariants
	if err := ledger.ValidateTransaction(tx.Entries); err != nil {
		return nil, err
	}

	if tx.IdempotencyKey == "" {
		return nil, ledger.ErrEmptyIdempotencyKey
	}

	// Step 2: Open PostgreSQL database transaction
	dbTx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to start database transaction: %w", err)
	}
	defer func() { _ = dbTx.Rollback(ctx) }()

	// Step 3: Check Idempotency Key
	existingTx, err := r.checkIdempotencyWithTx(ctx, dbTx, tx.IdempotencyKey)
	if err != nil && !errors.Is(err, ErrTransactionNotFound) {
		return nil, err
	}
	if existingTx != nil {
		return existingTx, nil // Idempotent return of already processed transaction
	}

	// Step 4: Collect and sort account IDs deterministically to avoid deadlocks
	accountMap := make(map[uuid.UUID]bool)
	for _, entry := range tx.Entries {
		accountMap[entry.AccountID] = true
	}

	sortedAccountIDs := make([]uuid.UUID, 0, len(accountMap))
	for id := range accountMap {
		sortedAccountIDs = append(sortedAccountIDs, id)
	}
	sort.Slice(sortedAccountIDs, func(i, j int) bool {
		return sortedAccountIDs[i].String() < sortedAccountIDs[j].String()
	})

	// Step 5: Acquire pessimistic row-level locks on all involved accounts in deterministic order
	lockedAccounts, err := r.lockAccountsForUpdate(ctx, dbTx, sortedAccountIDs)
	if err != nil {
		return nil, err
	}

	// Step 6: Calculate delta balance for each locked account
	// Standard Accounting Equation:
	// Asset / Expense: +Debit, -Credit
	// Liability / Equity / Revenue: +Credit, -Debit
	balanceDeltas := make(map[uuid.UUID]decimal.Decimal)
	for _, entry := range tx.Entries {
		acc, ok := lockedAccounts[entry.AccountID]
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrAccountNotFound, entry.AccountID)
		}

		delta := balanceDeltas[entry.AccountID]
		switch acc.Type {
		case ledger.AccountTypeAsset, ledger.AccountTypeExpense:
			if entry.Direction == ledger.DirectionDebit {
				delta = delta.Add(entry.Amount)
			} else {
				delta = delta.Sub(entry.Amount)
			}
		case ledger.AccountTypeLiability, ledger.AccountTypeEquity, ledger.AccountTypeRevenue:
			if entry.Direction == ledger.DirectionCredit {
				delta = delta.Add(entry.Amount)
			} else {
				delta = delta.Sub(entry.Amount)
			}
		default:
			return nil, fmt.Errorf("%w: %s", ledger.ErrInvalidAccountType, acc.Type)
		}
		balanceDeltas[entry.AccountID] = delta
	}

	// Step 7: Verify asset accounts do not overdraft if required
	now := time.Now().UTC()
	for accID, delta := range balanceDeltas {
		acc := lockedAccounts[accID]
		newBalance := acc.Balance.Add(delta)

		// Asset account safety: Cash/inventory accounts cannot become negative
		if acc.Type == ledger.AccountTypeAsset && newBalance.IsNegative() {
			return nil, fmt.Errorf("%w for account %s (%s): current=%s, delta=%s, resulting=%s",
				ErrInsufficientFunds, acc.Name, acc.ID, acc.Balance, delta, newBalance)
		}

		// Update account balance and increment version
		updateQuery := `
			UPDATE accounts
			SET balance = $1, version = version + 1, updated_at = $2
			WHERE id = $3
		`
		if _, err := dbTx.Exec(ctx, updateQuery, newBalance, now, accID); err != nil {
			return nil, fmt.Errorf("failed to update account balance for %s: %w", accID, err)
		}
	}

	// Step 8: Insert Transaction record
	if tx.ID == uuid.Nil {
		tx.ID = uuid.New()
	}
	tx.Status = ledger.TransactionStatusPosted
	tx.CreatedAt = now

	insertTxQuery := `
		INSERT INTO transactions (id, idempotency_key, description, status, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`
	if _, err := dbTx.Exec(ctx, insertTxQuery,
		tx.ID, tx.IdempotencyKey, tx.Description, string(tx.Status), tx.Metadata, tx.CreatedAt,
	); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // Unique violation on idempotency_key
			return nil, ErrDuplicateIdempotency
		}
		return nil, fmt.Errorf("failed to insert transaction: %w", err)
	}

	// Step 9: Insert immutable append-only Entries
	insertEntryQuery := `
		INSERT INTO entries (id, transaction_id, account_id, direction, amount, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`
	for i := range tx.Entries {
		if tx.Entries[i].ID == uuid.Nil {
			tx.Entries[i].ID = uuid.New()
		}
		tx.Entries[i].TransactionID = tx.ID
		tx.Entries[i].CreatedAt = now

		if _, err := dbTx.Exec(ctx, insertEntryQuery,
			tx.Entries[i].ID, tx.ID, tx.Entries[i].AccountID,
			string(tx.Entries[i].Direction), tx.Entries[i].Amount, tx.Entries[i].CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to insert entry[%d]: %w", i, err)
		}
	}

	// Step 10: Commit database transaction
	if err := dbTx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return tx, nil
}

func (r *LedgerRepository) checkIdempotencyWithTx(ctx context.Context, tx pgx.Tx, key string) (*ledger.Transaction, error) {
	query := `
		SELECT id, idempotency_key, description, status, metadata, created_at
		FROM transactions
		WHERE idempotency_key = $1
	`
	var t ledger.Transaction
	var status string

	err := tx.QueryRow(ctx, query, key).Scan(
		&t.ID, &t.IdempotencyKey, &t.Description, &status, &t.Metadata, &t.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTransactionNotFound
		}
		return nil, err
	}
	t.Status = ledger.TransactionStatus(status)
	return &t, nil
}

func (r *LedgerRepository) lockAccountsForUpdate(ctx context.Context, tx pgx.Tx, accountIDs []uuid.UUID) (map[uuid.UUID]*ledger.Account, error) {
	query := `
		SELECT id, name, type, currency, balance, version, created_at, updated_at
		FROM accounts
		WHERE id = ANY($1)
		FOR UPDATE
	`
	rows, err := tx.Query(ctx, query, accountIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to lock accounts for update: %w", err)
	}
	defer rows.Close()

	accounts := make(map[uuid.UUID]*ledger.Account)
	for rows.Next() {
		var a ledger.Account
		var accType string
		if err := rows.Scan(&a.ID, &a.Name, &accType, &a.Currency, &a.Balance, &a.Version, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan locked account: %w", err)
		}
		a.Type = ledger.AccountType(accType)
		accounts[a.ID] = &a
	}

	if len(accounts) != len(accountIDs) {
		return nil, fmt.Errorf("one or more accounts not found during lock acquisition")
	}

	return accounts, nil
}

type StatementEntryRow struct {
	EntryID        uuid.UUID
	TransactionID  uuid.UUID
	Description    string
	Direction      ledger.Direction
	Amount         decimal.Decimal
	CreatedAt      time.Time
}

// GetAccountEntriesForStatement retrieves chronological entries for an account within a date window.
func (r *LedgerRepository) GetAccountEntriesForStatement(
	ctx context.Context,
	accountID uuid.UUID,
	startDate, endDate time.Time,
) ([]StatementEntryRow, error) {
	query := `
		SELECT e.id, e.transaction_id, t.description, e.direction, e.amount, e.created_at
		FROM entries e
		JOIN transactions t ON e.transaction_id = t.id
		WHERE e.account_id = $1 AND e.created_at >= $2 AND e.created_at <= $3
		ORDER BY e.created_at ASC
	`
	rows, err := r.pool.Query(ctx, query, accountID, startDate, endDate)
	if err != nil {
		return nil, fmt.Errorf("failed to query statement entries: %w", err)
	}
	defer rows.Close()

	var entries []StatementEntryRow
	for rows.Next() {
		var row StatementEntryRow
		var dir string
		if err := rows.Scan(
			&row.EntryID, &row.TransactionID, &row.Description,
			&dir, &row.Amount, &row.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan statement row: %w", err)
		}
		row.Direction = ledger.Direction(dir)
		entries = append(entries, row)
	}
	return entries, nil
}

// GetAllAccounts retrieves all accounts in the ledger for trial balance calculations.
func (r *LedgerRepository) GetAllAccounts(ctx context.Context) ([]ledger.Account, error) {
	query := `
		SELECT id, name, type, currency, balance, version, created_at, updated_at
		FROM accounts
		ORDER BY type ASC, name ASC
	`
	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query all accounts: %w", err)
	}
	defer rows.Close()

	var accounts []ledger.Account
	for rows.Next() {
		var a ledger.Account
		var accType string
		if err := rows.Scan(&a.ID, &a.Name, &accType, &a.Currency, &a.Balance, &a.Version, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan account: %w", err)
		}
		a.Type = ledger.AccountType(accType)
		accounts = append(accounts, a)
	}
	return accounts, nil
}

