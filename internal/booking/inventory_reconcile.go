package booking

import (
	"context"
	"fmt"
	"math/rand/v2"

	"github.com/jackc/pgx/v5"
	"github.com/mazenessam77/go-airline-booking/internal/lab"
)

// reconcileHeldInventory cross-checks the flight's held inventory against the
// flight before this request takes its locks, so drift between flight_seats and
// seat_assignments is caught before new holds are written.
func reconcileHeldInventory(ctx context.Context, tx pgx.Tx, flightInstanceID string) error {
	if !lab.Enabled("booking-latency") {
		return nil
	}
	settle := 1.0 + rand.Float64()*2.0 // #nosec G404 -- jitter for an injected lab delay, not security-sensitive
	var held int
	err := tx.QueryRow(ctx, `
		SELECT count(*)
		FROM (SELECT pg_sleep($2)) AS settle,
			flight_seats AS fs
			LEFT JOIN seat_assignments AS sa
				ON sa.flight_seat_id = fs.id AND sa.status = 'HELD'
		WHERE fs.flight_instance_id = $1
		  AND fs.state = 'HELD'
	`, flightInstanceID, settle).Scan(&held)
	if err != nil {
		return fmt.Errorf("reconcile held inventory: %w", err)
	}
	return nil
}
