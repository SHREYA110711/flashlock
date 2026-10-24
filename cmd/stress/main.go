package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

type InventoryResponse struct {
	Item struct {
		SKU        string `json:"sku"`
		TotalStock int    `json:"total_stock"`
	} `json:"item"`
	AvailableStock int `json:"available_stock"`
}

type HoldRequest struct {
	SKU            string `json:"sku"`
	CustomerID     string `json:"customer_id"`
	IdempotencyKey string `json:"idempotency_key"`
	Quantity       int    `json:"quantity"`
	TTLSeconds     int    `json:"ttl_seconds"`
}

func main() {
	baseURL := flag.String("url", "http://localhost:8080", "Base URL of the FlashLock API server")
	targetSKU := flag.String("sku", "VIP-CONCERT-TICKET-2026", "Target SKU to contest")
	concurrency := flag.Int("concurrency", 500, "Number of concurrent buyers competing simultaneously")
	flag.Parse()

	fmt.Println("================================================================================")
	fmt.Println("⚡ FLASHLOCK HIGH-CONCURRENCY STRESS & ANTI-OVERSELL TEST")
	fmt.Println("================================================================================")
	fmt.Printf("🎯 Target Server  : %s\n", *baseURL)
	fmt.Printf("📦 Target SKU     : %s\n", *targetSKU)
	fmt.Printf("👥 Competitors    : %d concurrent buyers firing at the exact same millisecond\n", *concurrency)
	fmt.Println("--------------------------------------------------------------------------------")

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        1000,
			MaxIdleConnsPerHost: 1000,
			MaxConnsPerHost:     1000,
		},
	}

	// Step 1: Query initial stock
	resp, err := client.Get(fmt.Sprintf("%s/api/v1/inventory/%s", *baseURL, *targetSKU))
	if err != nil {
		fmt.Printf("❌ Failed to reach server at %s: %v\n", *baseURL, err)
		fmt.Println("💡 Make sure the FlashLock server is running: 'go run cmd/server/main.go'")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		fmt.Printf("❌ Failed to query SKU %s (HTTP %d): %s\n", *targetSKU, resp.StatusCode, string(body))
		return
	}

	var invData InventoryResponse
	if err := json.NewDecoder(resp.Body).Decode(&invData); err != nil {
		fmt.Printf("❌ Failed to decode inventory response: %v\n", err)
		return
	}

	initialStock := invData.AvailableStock
	fmt.Printf("📊 Initial Available Stock: %d units\n", initialStock)
	fmt.Println("🚀 Firing thundering-herd concurrent hold barrage now...")
	fmt.Println("--------------------------------------------------------------------------------")

	var (
		successCount int64
		conflictCount int64
		errorCount   int64
		startBarrier = make(chan struct{})
		wg           sync.WaitGroup
	)

	startTime := time.Now()

	// Launch N concurrent workers waiting at the start barrier
	for i := 0; i < *concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			// Prepare unique hold request with individual idempotency key
			holdReq := HoldRequest{
				SKU:            *targetSKU,
				CustomerID:     fmt.Sprintf("buyer-worker-%04d", workerID),
				IdempotencyKey: uuid.New().String(),
				Quantity:       1,
				TTLSeconds:     300,
			}
			reqBytes, _ := json.Marshal(holdReq)

			// Block until all goroutines are ready to fire simultaneously
			<-startBarrier

			req, _ := http.NewRequest("POST", fmt.Sprintf("%s/api/v1/reservations/hold", *baseURL), bytes.NewReader(reqBytes))
			req.Header.Set("Content-Type", "application/json")

			r, err := client.Do(req)
			if err != nil {
				atomic.AddInt64(&errorCount, 1)
				return
			}
			defer r.Body.Close()

			switch r.StatusCode {
			case http.StatusCreated:
				atomic.AddInt64(&successCount, 1)
			case http.StatusConflict:
				atomic.AddInt64(&conflictCount, 1)
			default:
				atomic.AddInt64(&errorCount, 1)
			}
		}(i)
	}

	// Release all workers simultaneously!
	close(startBarrier)
	wg.Wait()

	totalDuration := time.Since(startTime)
	rps := float64(*concurrency) / totalDuration.Seconds()

	fmt.Println("\n================================================================================")
	fmt.Println("📊 TEST RESULTS & INVARIANT VERIFICATION")
	fmt.Println("================================================================================")
	fmt.Printf("⏱️  Total Duration       : %v\n", totalDuration)
	fmt.Printf("⚡ Throughput            : %.2f requests/sec\n", rps)
	fmt.Printf("✅ Successful Holds     : %d\n", successCount)
	fmt.Printf("🛑 Rejected (No Stock)  : %d\n", conflictCount)
	fmt.Printf("⚠️  Network/Other Errors : %d\n", errorCount)
	fmt.Println("--------------------------------------------------------------------------------")

	// Verification Assertions
	if int(successCount) > initialStock {
		fmt.Printf("❌ CRITICAL INVARIANT BREACH: OVERSELLING DETECTED!\n")
		fmt.Printf("   Allocated %d holds but only %d units existed! (Oversold by %d)\n",
			successCount, initialStock, int(successCount)-initialStock)
	} else if int(successCount) == initialStock {
		fmt.Println("🏆 MATHEMATICAL PROOF PASSED: ZERO OVERSELLING!")
		fmt.Printf("   Exactly %d out of %d units were reserved. 0 oversold items under extreme contention.\n",
			successCount, initialStock)
	} else {
		fmt.Printf("ℹ️  Partially allocated: %d of %d units reserved.\n", successCount, initialStock)
	}
	fmt.Println("================================================================================")
}
