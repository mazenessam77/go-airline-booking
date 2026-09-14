package database

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func NewPostgresPool(
	ctx context.Context,
	databaseURL string,
	maxConnections int32,
	minConnections int32,
) (*pgxpool.Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf(
			"parse PostgreSQL configuration: %w",
			err,
		)
	}

	poolConfig.MaxConns = maxConnections
	poolConfig.MinConns = minConnections

	poolConfig.MaxConnLifetime = 30 * time.Minute
	poolConfig.MaxConnIdleTime = 5 * time.Minute
	poolConfig.HealthCheckPeriod = 30 * time.Second

	poolConfig.ConnConfig.RuntimeParams["application_name"] =
		"go-airline-booking-api"

	poolConfig.ConnConfig.RuntimeParams["statement_timeout"] =
		"5s"

	poolConfig.ConnConfig.RuntimeParams["lock_timeout"] =
		"1500ms"

	poolConfig.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = "5s"

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf(
			"create PostgreSQL pool: %w",
			err,
		)
	}

	pingContext, cancel := context.WithTimeout(
		ctx,
		5*time.Second,
	)
	defer cancel()

	if err := pool.Ping(pingContext); err != nil {
		pool.Close()

		return nil, fmt.Errorf(
			"ping PostgreSQL: %w",
			err,
		)
	}

	return pool, nil
}
