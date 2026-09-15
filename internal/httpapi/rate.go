package httpapi

import (
	"context"
	"crypto/sha256"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRateLimiter struct{ Pool *pgxpool.Pool }

func (l PostgresRateLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
	hash := sha256.Sum256([]byte(key))
	var count int
	err := l.Pool.QueryRow(ctx, `INSERT INTO rate_limit_buckets(key_hash,window_start,requests)
		VALUES($1,to_timestamp(floor(extract(epoch FROM clock_timestamp())/$2)*$2),1)
		ON CONFLICT(key_hash,window_start) DO UPDATE SET requests=LEAST(rate_limit_buckets.requests+1,$3+1)
		RETURNING requests`, hash[:], int64(window/time.Second), limit).Scan(&count)
	return count <= limit, err
}
