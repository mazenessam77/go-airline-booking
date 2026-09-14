package booking

import (
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const defaultSeatHoldDuration = 15 * time.Minute

type Store struct {
	pool             *pgxpool.Pool
	seatHoldDuration time.Duration
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{
		pool:             pool,
		seatHoldDuration: defaultSeatHoldDuration,
	}
}
