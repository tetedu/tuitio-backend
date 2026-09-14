// Package config loads service configuration from the environment.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	DatabaseURL      string
	RpcURL           string
	RegistryContract string
	EscrowContract   string
	StartLedger      uint32
	PollSeconds      int
	Port             string
	Host             string
	AllowedOrigins   []string
}

// Load reads configuration and fails fast on anything missing or malformed.
func Load() (Config, error) {
	c := Config{
		DatabaseURL:      os.Getenv("DATABASE_URL"),
		RpcURL:           os.Getenv("SOROBAN_RPC_URL"),
		RegistryContract: os.Getenv("REGISTRY_CONTRACT"),
		EscrowContract:   os.Getenv("ESCROW_CONTRACT"),
		Port:             getenv("PORT", "8080"),
		Host:             getenv("HOST", "0.0.0.0"),
	}
	if c.DatabaseURL == "" {
		return c, fmt.Errorf("DATABASE_URL is required")
	}
	if c.RpcURL == "" {
		return c, fmt.Errorf("SOROBAN_RPC_URL is required")
	}
	if c.RegistryContract == "" || c.EscrowContract == "" {
		return c, fmt.Errorf("REGISTRY_CONTRACT and ESCROW_CONTRACT are required")
	}

	var allowedOrigins []string
	if raw := os.Getenv("ALLOWED_ORIGINS"); raw != "" {
		for _, o := range strings.Split(raw, ",") {
			if o = strings.TrimSpace(o); o != "" {
				allowedOrigins = append(allowedOrigins, o)
			}
		}
	}

	poll, err := strconv.Atoi(getenv("POLL_SECONDS", "10"))
	if err != nil || poll < 1 {
		return c, fmt.Errorf("POLL_SECONDS must be a positive integer")
	}
	c.PollSeconds = poll

	start, err := strconv.ParseUint(getenv("START_LEDGER", "0"), 10, 32)
	if err != nil {
		return c, fmt.Errorf("START_LEDGER must be an unsigned 32-bit integer")
	}
	c.StartLedger = uint32(start)
	c.AllowedOrigins = allowedOrigins
	return c, nil
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
