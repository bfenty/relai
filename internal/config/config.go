package config

import (
	"os"
	"strconv"
)

// Config holds all application configuration.
type Config struct {
	Port                int
	Host                string
	DBPath              string
	LogDBURL            string
	RequirePostgresLogs bool
	AdminKey            string
	EnableCORS          bool
	AdminUsername       string
	AdminPassword       string // seed password on startup (env var only; not stored in config file)
	SecureCookies       bool   // set true when serving behind HTTPS
	ReadyRequireModels  bool
	// AdmissionTimeoutSeconds bounds queueing before a backend slot is acquired.
	AdmissionTimeoutSeconds int
	// StreamResponseHeaderTimeoutSeconds permits expensive prompt evaluation before SSE begins.
	StreamResponseHeaderTimeoutSeconds int
	// StreamIdleTimeoutSeconds cancels a streaming request that stops making progress.
	StreamIdleTimeoutSeconds int
}

// Load reads configuration from environment variables with defaults.
func Load() *Config {
	port := envInt("RELAI_PORT", 8080)
	return &Config{
		Port:                               port,
		Host:                               env("RELAI_HOST", "0.0.0.0"),
		DBPath:                             env("RELAI_DB_PATH", "./relai.db"),
		LogDBURL:                           env("RELAI_LOG_DB_URL", ""),
		RequirePostgresLogs:                os.Getenv("RELAI_REQUIRE_POSTGRES_LOGS") == "true",
		AdminKey:                           env("RELAI_ADMIN_KEY", ""),
		EnableCORS:                         true,
		AdminUsername:                      env("RELAI_ADMIN_USERNAME", "admin"),
		AdminPassword:                      env("RELAI_ADMIN_PASSWORD", ""),
		SecureCookies:                      os.Getenv("RELAI_SECURE_COOKIES") == "true",
		ReadyRequireModels:                 os.Getenv("RELAI_READY_REQUIRE_MODELS") == "true",
		AdmissionTimeoutSeconds:            envInt("RELAI_ADMISSION_TIMEOUT_SECONDS", 300),
		StreamResponseHeaderTimeoutSeconds: envInt("RELAI_STREAM_RESPONSE_HEADER_TIMEOUT_SECONDS", 900),
		StreamIdleTimeoutSeconds:           envInt("RELAI_STREAM_IDLE_TIMEOUT_SECONDS", 120),
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}
