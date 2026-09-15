package booking

import (
	"context"
	"github.com/mazenessam77/go-airline-booking/internal/validate"
)

func (s *Store) List(ctx context.Context, user, after string, limit int) ([]Summary, error) {
	if !validate.UUID(user) || limit < 1 || limit > 50 || (after != "" && !validate.UUID(after)) {
		return nil, ErrInvalidHoldRequest
	}
	var cursor any
	if after != "" {
		cursor = after
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text,pnr,status,currency,total_minor,version,hold_expires_at
		FROM bookings WHERE user_id=$1 AND ($2::uuid IS NULL OR (created_at,id)<
		(SELECT created_at,id FROM bookings WHERE id=$2 AND user_id=$1)) ORDER BY created_at DESC,id DESC LIMIT $3`, user, cursor, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Summary{}
	for rows.Next() {
		var v Summary
		if err = rows.Scan(&v.ID, &v.PNR, &v.Status, &v.Currency, &v.TotalMinor, &v.Version, &v.HoldExpiresAt); err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}
