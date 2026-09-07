package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	HTTPAddr                 string
	DatabaseURL              string
	JWTSecret                string
	JWTTTL                   time.Duration
	ServiceJWTTTL            time.Duration
	BcryptCost               int
	LiquidaExternal          bool
	AllowPublicAdminRegister bool
	AdminEmail               string
	AdminPassword            string
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

	serviceTTL, err := time.ParseDuration(getenv("SERVICE_JWT_TTL", "15m"))
	if err != nil {
		return Config{}, fmt.Errorf("SERVICE_JWT_TTL inválido: %w", err)
	}
	cfg.ServiceJWTTTL = serviceTTL

	cost, err := strconv.Atoi(getenv("BCRYPT_COST", "10"))
	if err != nil {
		return Config{}, fmt.Errorf("BCRYPT_COST inválido: %w", err)
	}
	cfg.BcryptCost = cost

	mode := getenv("LIQUIDA_INTEGRATION", "standalone")
	switch mode {
	case "standalone":
		cfg.LiquidaExternal = false
	case "external":
		cfg.LiquidaExternal = true
	default:
		return Config{}, fmt.Errorf("LIQUIDA_INTEGRATION inválido: %q (use standalone ou external)", mode)
	}

	allowAdmin, err := strconv.ParseBool(getenv("ALLOW_PUBLIC_ADMIN_REGISTER", "false"))
	if err != nil {
		return Config{}, fmt.Errorf("ALLOW_PUBLIC_ADMIN_REGISTER inválido: %w", err)
	}
	cfg.AllowPublicAdminRegister = allowAdmin

	cfg.AdminEmail = os.Getenv("ADMIN_EMAIL")
	cfg.AdminPassword = os.Getenv("ADMIN_PASSWORD")

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
