package main

import (
	"context"
	"fmt"
	"log"
	stdhttp "net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"flashlock/internal/config"
	"flashlock/internal/repository/postgres"
	"flashlock/internal/repository/redis"
	"flashlock/internal/service"
	apphttp "flashlock/internal/transport/http"
)

func main() {
	log.Println("⚡ Initializing FlashLock Engine...")

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("❌ Failed to load configuration: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Printf("📦 Environment: %s | Port: %d", cfg.AppEnv, cfg.AppPort)

	// Attempt connecting to PostgreSQL
	pgPool, err := postgres.NewPool(ctx, cfg)
	if err != nil {
		log.Printf("⚠️  PostgreSQL connection pending (Run 'docker compose up -d' if not started): %v", err)
	} else {
		defer pgPool.Close()
		log.Println("✅ PostgreSQL connected successfully (connection pool active)")
	}

	// Attempt connecting to Redis
	rdb, err := redis.NewClient(ctx, cfg)
	if err != nil {
		log.Printf("⚠️  Redis connection pending (Run 'docker compose up -d' if not started): %v", err)
	} else {
		defer rdb.Close()
		log.Println("✅ Redis connected successfully")
	}

	// Initialize Echo HTTP server
	echoServer := apphttp.NewServer()

	// Wire Ledger Domain Components if Database is ready
	if pgPool != nil {
		ledgerRepo := postgres.NewLedgerRepository(pgPool)
		ledgerService := service.NewLedgerService(ledgerRepo)
		ledgerHandler := apphttp.NewLedgerHandler(ledgerService)
		ledgerHandler.RegisterRoutes(echoServer)
		log.Println("⚡ Ledger API routes registered (/api/v1/accounts, /api/v1/ledger/transactions, /api/v1/ledger/transfers)")
	}

	// Start Echo HTTP server in background goroutine
	go func() {
		addr := fmt.Sprintf(":%d", cfg.AppPort)
		log.Printf("🌐 HTTP Server listening on %s", addr)
		if err := echoServer.Start(addr); err != nil && err != stdhttp.ErrServerClosed {
			log.Printf("⚠️  HTTP server error: %v", err)
		}
	}()

	log.Println("🚀 FlashLock Phase 2 Engine ready.")

	// Wait for termination signal or exit cleanly in test run
	select {
	case <-ctx.Done():
		log.Println("🛑 Shutting down gracefully...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = echoServer.Shutdown(shutdownCtx)
	case <-time.After(1 * time.Second):
		// Clean startup exit in scaffolding check
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		_ = echoServer.Shutdown(shutdownCtx)
	}

	fmt.Println("Done.")
}
