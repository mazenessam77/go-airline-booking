package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	HTTPAddr    string
	DatabaseURL string
	DBMaxConns  int32
	DBMinConns  int32
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
		HTTPAddr:    readString("HTTP_ADDR", ":8080"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
		DBMaxConns:  maxConnections,
		DBMinConns:  minConnections,
	}

	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}

	if cfg.DBMinConns > cfg.DBMaxConns {
		return Config{}, errors.New(
			"DB_MIN_CONNS cannot exceed DB_MAX_CONNS",
		)
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
