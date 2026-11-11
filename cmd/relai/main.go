package main

import (
	"crypto/rand"
	"encoding/hex"
	"log"

	"github.com/bfenty/relai/internal/config"
	"github.com/bfenty/relai/internal/db"
	"github.com/bfenty/relai/internal/server"
	"github.com/bfenty/relai/internal/server/handlers"
)

func main() {
	cfg := config.Load()

	database, err := db.New(cfg.DBPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	if cfg.RequirePostgresLogs && cfg.LogDBURL == "" {
		log.Fatalf("RELAI_REQUIRE_POSTGRES_LOGS=true but RELAI_LOG_DB_URL is empty")
	}
	if cfg.LogDBURL != "" {
		if err := database.UsePostgresLogs(cfg.LogDBURL); err != nil {
			log.Fatalf("Failed to open PostgreSQL log database: %v", err)
		}
		log.Printf("Request logging configured for PostgreSQL")
	}

	if err := seedAdminCredentials(cfg, database); err != nil {
		log.Fatalf("Failed to seed admin credentials: %v", err)
	}

	srv := server.New(cfg, database)
	srv.SetupRoutes()

	if err := srv.Run(); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

// seedAdminCredentials ensures an admin login exists.
// - If RELAI_ADMIN_PASSWORD is set, it always upserts that password.
// - If no credentials exist in DB yet, a random password is generated and printed once.
func seedAdminCredentials(cfg *config.Config, d *db.DB) error {
	if cfg.AdminPassword != "" {
		hash, err := handlers.HashPassword(cfg.AdminPassword)
		if err != nil {
			return err
		}
		if err := d.AdminCreds.Upsert(cfg.AdminUsername, hash); err != nil {
			return err
		}
		log.Printf("Admin credentials set for user %q (from RELAI_ADMIN_PASSWORD)", cfg.AdminUsername)
		return nil
	}

	// No env var — check if credentials already exist
	has, err := d.AdminCreds.HasAny()
	if err != nil {
		return err
	}
	if has {
		return nil // Use existing stored credentials
	}

	// First run: generate a random password and print it
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return err
	}
	password := hex.EncodeToString(b)

	hash, err := handlers.HashPassword(password)
	if err != nil {
		return err
	}
	if err := d.AdminCreds.Upsert(cfg.AdminUsername, hash); err != nil {
		return err
	}

	log.Printf("===========================================")
	log.Printf("  Admin credentials (first run)")
	log.Printf("  Username: %s", cfg.AdminUsername)
	log.Printf("  Password: %s", password)
	log.Printf("  Set RELAI_ADMIN_PASSWORD to override.")
	log.Printf("===========================================")

	return nil
}
