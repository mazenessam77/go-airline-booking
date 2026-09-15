package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mazenessam77/go-airline-booking/internal/auth"
	"github.com/mazenessam77/go-airline-booking/internal/booking"
	"github.com/mazenessam77/go-airline-booking/internal/config"
	"github.com/mazenessam77/go-airline-booking/internal/database"
	"github.com/mazenessam77/go-airline-booking/internal/flight"
	"github.com/mazenessam77/go-airline-booking/internal/httpapi"
	"github.com/mazenessam77/go-airline-booking/internal/observability"
	"github.com/mazenessam77/go-airline-booking/internal/payment"
	"github.com/mazenessam77/go-airline-booking/internal/quote"
	"github.com/mazenessam77/go-airline-booking/internal/webui"
)

func main() {
	logger := observability.NewLogger(os.Stdout)

	if err := run(logger); err != nil {
		logger.Error("application stopped", "code", "startup_failed")

		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}

	appContext, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()

	pool, err := database.NewPostgresPool(
		appContext,
		cfg.DatabaseURL,
		cfg.DBMaxConns,
		cfg.DBMinConns,
	)
	if err != nil {
		return err
	}
	defer pool.Close()

	mux := http.NewServeMux()
	authHTTP := httpapi.AuthHandler{Store: auth.NewStore(pool), Limiter: httpapi.PostgresRateLimiter{Pool: pool}, TrustedProxies: cfg.TrustedProxies}
	authHTTP.RegisterRoutes(mux)
	httpapi.RegisterFlightRoutes(mux, flight.Store{Pool: pool})
	httpapi.RegisterBookingRoutes(mux, authHTTP, booking.NewStore(pool), quote.Store{Pool: pool})
	httpapi.RegisterPaymentRoutes(mux, authHTTP, booking.NewStore(pool), payment.Store{Pool: pool}, nil)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		httpapi.Error(w, r, 404, "not_found", "Resource not found")
	})

	mux.HandleFunc("GET /healthz", func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		writeJSON(writer, http.StatusOK, map[string]string{
			"status": "ok",
		})
	})

	mux.HandleFunc("GET /readyz", func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		ctx, cancel := context.WithTimeout(
			request.Context(),
			2*time.Second,
		)
		defer cancel()

		if err := pool.Ping(ctx); err != nil {
			writeJSON(
				writer,
				http.StatusServiceUnavailable,
				map[string]string{
					"status": "unavailable",
				},
			)
			return
		}

		writeJSON(writer, http.StatusOK, map[string]string{
			"status": "ready",
		})
	})

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           webui.Handler(httpapi.New(mux, httpapi.Options{Logger: logger, Timeout: cfg.RequestTimeout, BodyLimit: cfg.BodyLimit, AllowedOrigins: cfg.AllowedOrigins, TrustedProxies: cfg.TrustedProxies})),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      cfg.RequestTimeout + 5*time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	serverErrors := make(chan error, 1)

	go func() {
		logger.Info(
			"HTTP server started",
			"address",
			cfg.HTTPAddr,
		)

		serverErrors <- server.ListenAndServe()
	}()

	select {
	case <-appContext.Done():
		logger.Info("shutdown signal received")

	case serverError := <-serverErrors:
		if !errors.Is(serverError, http.ErrServerClosed) {
			return fmt.Errorf(
				"serve HTTP: %w",
				serverError,
			)
		}
	}

	shutdownContext, cancel := context.WithTimeout(
		context.Background(),
		10*time.Second,
	)
	defer cancel()

	if err := server.Shutdown(shutdownContext); err != nil {
		return fmt.Errorf(
			"graceful shutdown: %w",
			err,
		)
	}

	return nil
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set(
			"X-Content-Type-Options",
			"nosniff",
		)

		writer.Header().Set(
			"X-Frame-Options",
			"DENY",
		)

		writer.Header().Set(
			"Cache-Control",
			"no-store",
		)

		next.ServeHTTP(writer, request)
	})
}

func writeJSON(
	writer http.ResponseWriter,
	status int,
	value any,
) {
	writer.Header().Set(
		"Content-Type",
		"application/json",
	)

	writer.WriteHeader(status)

	_ = json.NewEncoder(writer).Encode(value)
}
