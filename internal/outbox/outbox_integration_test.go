package outbox

import (
	"context"
	"errors"
	"testing"

	"github.com/mazenessam77/go-airline-booking/internal/testsupport"
)

type failingPublisher struct{}

func (failingPublisher) Publish(context.Context, Event) error { return errors.New("test failure") }
func TestOutboxConcurrentAndRetry(t *testing.T) {
	ctx, pool := testsupport.Open(t)
	f := testsupport.Seed(t, ctx, pool)
	if _, err := pool.Exec(ctx, `INSERT INTO outbox_events(aggregate_id,event_type) SELECT $1,'TEST' FROM generate_series(1,20)`, f.Booking); err != nil {
		t.Fatal(err)
	}
	p := Processor{Pool: pool, Publisher: ReceiptConsumer{Pool: pool, Name: "test"}}
	results := make(chan error, 2)
	for range 2 {
		go func() { _, err := p.Batch(ctx, 20); results <- err }()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_receipts WHERE consumer='test'`).Scan(&count); err != nil || count != 20 {
		t.Fatalf("receipts %d: %v", count, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO outbox_events(aggregate_id,event_type,attempts) VALUES($1,'FAIL',9)`, f.Booking); err != nil {
		t.Fatal(err)
	}
	p.Publisher = failingPublisher{}
	if _, err := p.Batch(ctx, 20); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE dead_lettered_at IS NOT NULL`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("dead letters %d: %v", count, err)
	}
}
