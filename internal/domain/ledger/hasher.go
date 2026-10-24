package ledger

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

const GenesisHash = "0000000000000000000000000000000000000000000000000000000000000000"

// ComputeTransactionHash calculates a deterministic SHA-256 cryptographic hash of a transaction.
// Hashing components:
// 1. previous_hash (Merkle chain linkage)
// 2. transaction ID & idempotency_key
// 3. Unix timestamp (seconds)
// 4. Sorted entry legs (account_id, direction, amount)
func ComputeTransactionHash(previousHash string, tx *Transaction) string {
	if previousHash == "" {
		previousHash = GenesisHash
	}

	// Format entry legs deterministically
	entryStrings := make([]string, len(tx.Entries))
	for i, e := range tx.Entries {
		entryStrings[i] = fmt.Sprintf("%s:%s:%s", e.AccountID.String(), string(e.Direction), e.Amount.StringFixed(4))
	}
	sort.Strings(entryStrings)
	entriesDigest := strings.Join(entryStrings, "|")

	rawPayload := fmt.Sprintf("%s:%s:%s:%d:%s",
		previousHash,
		tx.ID.String(),
		tx.IdempotencyKey,
		tx.CreatedAt.Unix(),
		entriesDigest,
	)

	hasher := sha256.New()
	hasher.Write([]byte(rawPayload))
	return hex.EncodeToString(hasher.Sum(nil))
}

// VerifyTransactionHash validates if a transaction's current_hash matches its content and previous_hash.
func VerifyTransactionHash(previousHash, currentHash string, tx *Transaction) bool {
	computed := ComputeTransactionHash(previousHash, tx)
	return computed == currentHash
}
