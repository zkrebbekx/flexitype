// Package config loads flexitype's service configuration from FLEXITYPE_*
// environment variables — twelve-factor style, no config files required.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config is the standalone service configuration.
type Config struct {
	// Port the HTTP server listens on.
	Port int
	// Database connection settings.
	Database Database
	// ServiceAccountsPath points at the service-account JSON file. Empty
	// disables authentication (development only).
	ServiceAccountsPath string
	// LogLevel and LogFormat feed the logger.
	LogLevel  string
	LogFormat string
	// ShutdownTimeout bounds graceful shutdown.
	ShutdownTimeout time.Duration
	// MigrateOnStart applies embedded migrations during boot.
	MigrateOnStart bool
}

// Database holds PostgreSQL pool settings.
type Database struct {
	Host            string
	Port            int
	User            string
	Password        string
	Name            string
	SSLMode         string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

// DSN renders the lib/pq connection string.
func (d Database) DSN() string {
	return fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		d.Host, d.Port, d.User, d.Password, d.Name, d.SSLMode)
}

// Load reads configuration from the environment with production-safe
// defaults.
func Load() (Config, error) {
	cfg := Config{
		Port:                envInt("FLEXITYPE_PORT", 8080),
		ServiceAccountsPath: os.Getenv("FLEXITYPE_SERVICE_ACCOUNTS"),
		LogLevel:            envStr("FLEXITYPE_LOG_LEVEL", "info"),
		LogFormat:           envStr("FLEXITYPE_LOG_FORMAT", "json"),
		ShutdownTimeout:     envDuration("FLEXITYPE_SHUTDOWN_TIMEOUT", 30*time.Second),
		MigrateOnStart:      envBool("FLEXITYPE_MIGRATE_ON_START", true),
		Database: Database{
			Host:            envStr("FLEXITYPE_DB_HOST", "localhost"),
			Port:            envInt("FLEXITYPE_DB_PORT", 5432),
			User:            envStr("FLEXITYPE_DB_USER", "postgres"),
			Password:        envStr("FLEXITYPE_DB_PASSWORD", "postgres"),
			Name:            envStr("FLEXITYPE_DB_NAME", "flexitype"),
			SSLMode:         envStr("FLEXITYPE_DB_SSLMODE", "disable"),
			MaxOpenConns:    envInt("FLEXITYPE_DB_MAX_OPEN_CONNS", 25),
			MaxIdleConns:    envInt("FLEXITYPE_DB_MAX_IDLE_CONNS", 10),
			ConnMaxLifetime: envDuration("FLEXITYPE_DB_CONN_MAX_LIFETIME", 30*time.Minute),
		},
	}
	if cfg.Port <= 0 || cfg.Port > 65535 {
		return Config{}, fmt.Errorf("invalid FLEXITYPE_PORT %d", cfg.Port)
	}
	return cfg, nil
}

func envStr(key, fallback string) string {
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

func envBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}
