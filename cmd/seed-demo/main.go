package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mazenessam77/go-airline-booking/internal/config"
	"github.com/mazenessam77/go-airline-booking/internal/demo"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.Environment != "development" {
		return fmt.Errorf("demo seeding is restricted to APP_ENV=development")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("cannot configure demo database")
	}
	defer pool.Close()
	var name string
	if err = pool.QueryRow(ctx, `SELECT current_database()`).Scan(&name); err != nil {
		return fmt.Errorf("cannot connect to demo database")
	}
	if name != "airline_booking_demo" {
		return fmt.Errorf("refusing to seed outside airline_booking_demo")
	}
	result, err := demo.Seed(ctx, pool, time.Now())
	if err != nil {
		return fmt.Errorf("demo seed failed; ensure migrations are applied to the isolated demo database")
	}
	fmt.Printf("Demo data ready: %d new flights, %d new seats. Existing data preserved.\n", result.FlightsAdded, result.SeatsAdded)
	return nil
}
