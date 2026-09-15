package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mazenessam77/go-airline-booking/internal/booking"
	"github.com/mazenessam77/go-airline-booking/internal/config"
	"github.com/mazenessam77/go-airline-booking/internal/database"
	"github.com/mazenessam77/go-airline-booking/internal/observability"
	"github.com/mazenessam77/go-airline-booking/internal/outbox"
	"github.com/mazenessam77/go-airline-booking/internal/payment"
)

type dispatcher struct {
	receipts outbox.ReceiptConsumer
	payments payment.Store
}

func (d dispatcher) Publish(ctx context.Context, e outbox.Event) error {
	switch e.Type {
	case "PAYMENT_REQUESTED":
		return d.payments.Dispatch(ctx, e.AggregateID)
	case "REFUND_REQUESTED":
		return d.payments.DispatchRefund(ctx, e.AggregateID)
	}
	return d.receipts.Publish(ctx, e)
}

func main() {
	logger := observability.NewLogger(os.Stdout)
	if err := run(logger); err != nil {
		logger.Error("worker stopped", "code", "worker_failed")
		os.Exit(1)
	}
}
func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	pool, err := database.NewPostgresPool(ctx, cfg.DatabaseURL, cfg.DBMaxConns, cfg.DBMinConns)
	if err != nil {
		return err
	}
	defer pool.Close()
	bookings := booking.NewStore(pool)
	processor := outbox.Processor{Pool: pool, Publisher: dispatcher{receipts: outbox.ReceiptConsumer{Pool: pool, Name: "domain-ledger"}, payments: payment.Store{Pool: pool}}}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	logger.Info("worker started")
	for {
		select {
		case <-ctx.Done():
			logger.Info("worker stopped gracefully")
			return nil
		case <-ticker.C:
		}
		jobCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		count, err := bookings.ExpireHolds(jobCtx, 25)
		if err != nil && ctx.Err() == nil {
			logger.Error("hold expiration failed", "code", "expiration_failed")
		} else if count > 0 {
			logger.Info("holds expired", "count", count)
		}
		if _, err = processor.Batch(jobCtx, 10); err != nil && ctx.Err() == nil {
			logger.Error("outbox batch failed", "code", "outbox_failed")
		}
		cancel()
	}
}
