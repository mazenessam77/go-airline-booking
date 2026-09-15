package config

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	HTTPAddr       string
	DatabaseURL    string
	DBMaxConns     int32
	DBMinConns     int32
	Environment    string
	RequestTimeout time.Duration
	BodyLimit      int64
	AllowedOrigins []string
	TrustedProxies []netip.Prefix
}

func Load() (Config, error) {
	maxConnections, err := readInt32("DB_MAX_CONNS", 20)
	if err != nil {
		return Config{}, err
	}

	minConnections, err := readInt32("DB_MIN_CONNS", 2)
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		HTTPAddr:       readString("HTTP_ADDR", ":8080"),
		DatabaseURL:    os.Getenv("DATABASE_URL"),
		DBMaxConns:     maxConnections,
		DBMinConns:     minConnections,
		Environment:    readString("APP_ENV", "development"),
		RequestTimeout: 10 * time.Second,
		BodyLimit:      64 << 10,
	}

	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}

	if cfg.DBMinConns > cfg.DBMaxConns {
		return Config{}, errors.New(
			"DB_MIN_CONNS cannot exceed DB_MAX_CONNS",
		)
	}
	if cfg.Environment != "development" && cfg.Environment != "test" && cfg.Environment != "production" {
		return Config{}, errors.New("APP_ENV must be development, test, or production")
	}
	if raw := os.Getenv("REQUEST_TIMEOUT"); raw != "" {
		cfg.RequestTimeout, err = time.ParseDuration(raw)
		if err != nil || cfg.RequestTimeout < time.Second || cfg.RequestTimeout > 30*time.Second {
			return Config{}, errors.New("REQUEST_TIMEOUT must be between 1s and 30s")
		}
	}
	if raw := os.Getenv("REQUEST_BODY_LIMIT"); raw != "" {
		cfg.BodyLimit, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || cfg.BodyLimit < 1024 || cfg.BodyLimit > 1<<20 {
			return Config{}, errors.New("REQUEST_BODY_LIMIT must be between 1024 and 1048576")
		}
	}
	for _, raw := range strings.Split(os.Getenv("CORS_ALLOWED_ORIGINS"), ",") {
		if raw = strings.TrimSpace(raw); raw == "" {
			continue
		}
		u, e := url.Parse(raw)
		if e != nil || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && (cfg.Environment == "production" || u.Scheme != "http")) {
			return Config{}, errors.New("invalid CORS_ALLOWED_ORIGINS")
		}
		cfg.AllowedOrigins = append(cfg.AllowedOrigins, raw)
	}
	for _, raw := range strings.Split(os.Getenv("TRUSTED_PROXY_CIDRS"), ",") {
		if raw = strings.TrimSpace(raw); raw == "" {
			continue
		}
		prefix, e := netip.ParsePrefix(raw)
		if e != nil || prefix.Bits() == 0 {
			return Config{}, errors.New("invalid TRUSTED_PROXY_CIDRS")
		}
		cfg.TrustedProxies = append(cfg.TrustedProxies, prefix)
	}
	// Production connections use a URL so TLS policy can be validated explicitly.
	u, e := url.Parse(cfg.DatabaseURL)
	parsed, parseErr := pgxpool.ParseConfig(cfg.DatabaseURL)
	if parseErr != nil {
		return Config{}, errors.New("invalid DATABASE_URL")
	}
	host := parsed.ConnConfig.Host
	local := host == "localhost" || host == "127.0.0.1" || host == "::1" || host == "postgres"
	if (cfg.Environment == "production" || !local) && (e != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Query().Get("sslmode") != "verify-full" || u.Query().Get("sslrootcert") == "") {
		return Config{}, errors.New("non-local DATABASE_URL requires sslmode=verify-full and sslrootcert")
	}

	return cfg, nil
}

func readString(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func readInt32(key string, fallback int32) (int32, error) {
	rawValue := os.Getenv(key)

	if rawValue == "" {
		return fallback, nil
	}

	value, err := strconv.ParseInt(rawValue, 10, 32)
	if err != nil || value < 1 {
		return 0, fmt.Errorf(
			"%s must be a positive integer",
			key,
		)
	}

	return int32(value), nil
}
