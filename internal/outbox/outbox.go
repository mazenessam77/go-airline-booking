package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Event struct {
	ID, AggregateID, Type, Lease string
	Payload                      json.RawMessage
}
type Publisher interface {
	Publish(context.Context, Event) error
}
type Processor struct {
	Pool      *pgxpool.Pool
	Publisher Publisher
}

func (p Processor) Batch(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 100 || p.Publisher == nil {
		return 0, errors.New("invalid outbox processor configuration")
	}
	rows, err := p.Pool.Query(ctx, `WITH claimed AS (
		SELECT id FROM outbox_events WHERE delivered_at IS NULL AND dead_lettered_at IS NULL
		AND next_attempt_at<=clock_timestamp() AND (locked_until IS NULL OR locked_until<=clock_timestamp())
		ORDER BY next_attempt_at,id LIMIT $1 FOR UPDATE SKIP LOCKED
	) UPDATE outbox_events e SET locked_until=clock_timestamp()+interval '30 seconds',lock_token=gen_random_uuid(),attempts=attempts+1
	FROM claimed WHERE e.id=claimed.id RETURNING e.id::text,e.aggregate_id::text,e.event_type,e.payload,e.lock_token::text`, limit)
	if err != nil {
		return 0, err
	}
	events := []Event{}
	for rows.Next() {
		var e Event
		if err = rows.Scan(&e.ID, &e.AggregateID, &e.Type, &e.Payload, &e.Lease); err != nil {
			rows.Close()
			return 0, err
		}
		events = append(events, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	for _, e := range events {
		publishCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		publishErr := p.Publisher.Publish(publishCtx, e)
		cancel()
		if publishErr == nil {
			_, err = p.Pool.Exec(ctx, `UPDATE outbox_events SET delivered_at=clock_timestamp(),locked_until=NULL,lock_token=NULL WHERE id=$1 AND lock_token=$2`, e.ID, e.Lease)
		} else {
			_, err = p.Pool.Exec(ctx, `UPDATE outbox_events SET locked_until=NULL,lock_token=NULL,
				next_attempt_at=clock_timestamp()+make_interval(secs=>LEAST(3600,power(2,LEAST(attempts,12)))::double precision),
				dead_lettered_at=CASE WHEN attempts>=10 THEN clock_timestamp() END WHERE id=$1 AND lock_token=$2`, e.ID, e.Lease)
		}
		if err != nil {
			return 0, err
		}
	}
	return len(events), nil
}

// ReceiptConsumer records durable processing without claiming to send notifications.
// External consumers should commit their own receipt with their database side effect.
type ReceiptConsumer struct {
	Pool *pgxpool.Pool
	Name string
}

func (c ReceiptConsumer) Publish(ctx context.Context, e Event) error {
	_, err := c.Pool.Exec(ctx, `INSERT INTO outbox_receipts(consumer,event_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, c.Name, e.ID)
	return err
}
