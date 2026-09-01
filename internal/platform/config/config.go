package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	HTTPAddr    string
	DatabaseURL string
	JWTSecret   string
	JWTTTL      time.Duration
	BcryptCost  int
}

func Load() (Config, error) {
	cfg := Config{
		HTTPAddr:    getenv("HTTP_ADDR", ":8080"),
		DatabaseURL: getenv("DATABASE_URL", "postgres://bankcore:bankcore@localhost:5432/bankcore?sslmode=disable"),
		JWTSecret:   os.Getenv("JWT_SECRET"),
	}

	ttl, err := time.ParseDuration(getenv("JWT_TTL", "24h"))
	if err != nil {
		return Config{}, fmt.Errorf("JWT_TTL inválido: %w", err)
	}
	cfg.JWTTTL = ttl

	cost, err := strconv.Atoi(getenv("BCRYPT_COST", "10"))
	if err != nil {
		return Config{}, fmt.Errorf("BCRYPT_COST inválido: %w", err)
	}
	cfg.BcryptCost = cost

	if cfg.JWTSecret == "" {
		cfg.JWTSecret = "dev-insecure-secret-change-me"
	}

	return cfg, nil
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
