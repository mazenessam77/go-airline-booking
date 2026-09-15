package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

type contextKey struct{}

type Options struct {
	Observer       Observer
	Logger         *slog.Logger
	Timeout        time.Duration
	BodyLimit      int64
	AllowedOrigins []string
	TrustedProxies []netip.Prefix
}

type ErrorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

func RequestID(ctx context.Context) string { id, _ := ctx.Value(contextKey{}).(string); return id }

func JSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func Error(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	JSON(w, status, struct {
		Error ErrorBody `json:"error"`
	}{ErrorBody{code, message, RequestID(r.Context())}})
}

func Decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		Error(w, r, 415, "unsupported_media_type", "Content-Type must be application/json")
		return false
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	err = decoder.Decode(dst)
	if err == nil {
		var extra any
		if err = decoder.Decode(&extra); errors.Is(err, io.EOF) {
			return true
		}
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		Error(w, r, 413, "body_too_large", "Request body is too large")
	} else {
		Error(w, r, 400, "invalid_json", "Expected one valid JSON document with known fields")
	}
	return false
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *responseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *responseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(p)
}

func New(next http.Handler, opts Options) http.Handler {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Second
	}
	if opts.BodyLimit <= 0 {
		opts.BodyLimit = 64 << 10
	}
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recover() != nil {
				opts.Logger.Error("request panic", "request_id", RequestID(r.Context()))
				Error(w, r, 500, "internal_error", "An internal error occurred")
			}
		}()
		origin := r.Header.Get("Origin")
		if origin != "" {
			w.Header().Add("Vary", "Origin")
			// Same-origin browser requests need no CORS grant. Forwarded headers are not trusted here.
			scheme := "http"
			if r.TLS != nil {
				scheme = "https"
			}
			allowed := origin == scheme+"://"+r.Host
			for _, value := range opts.AllowedOrigins {
				if value == origin {
					allowed = true
					break
				}
			}
			if !allowed {
				Error(w, r, 403, "origin_denied", "Origin is not allowed")
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Expose-Headers", "X-Request-ID, Retry-After")
			if r.Method == http.MethodOptions {
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key")
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		r.Body = http.MaxBytesReader(w, r.Body, opts.BodyLimit)
		next.ServeHTTP(w, r)
	})
	timed := http.TimeoutHandler(inner, opts.Timeout, `{"error":{"code":"request_timeout","message":"Request timed out"}}`)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var id [16]byte
		if _, err := rand.Read(id[:]); err != nil {
			Error(w, r, 503, "unavailable", "Service temporarily unavailable")
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), contextKey{}, hex.EncodeToString(id[:])))
		w.Header().Set("X-Request-ID", RequestID(r.Context()))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		writer := &responseWriter{ResponseWriter: w}
		start := time.Now()
		timed.ServeHTTP(writer, r)
		if opts.Observer != nil {
			opts.Observer.ObserveRequest(r.Context(), "http", writer.status, time.Since(start))
		}
		// Paths, queries, headers, bodies and client addresses may contain secrets or PII.
		opts.Logger.Info("http request", "request_id", RequestID(r.Context()), "status", writer.status, "duration_ms", time.Since(start).Milliseconds())
	})
}

// ClientIP walks forwarding information only through explicitly trusted peers.
func ClientIP(r *http.Request, trusted []netip.Prefix) string {
	peer, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		return "unknown"
	}
	current := peer.Addr().Unmap()
	isTrusted := func(addr netip.Addr) bool {
		for _, prefix := range trusted {
			if prefix.Contains(addr) {
				return true
			}
		}
		return false
	}
	chain := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	if len(chain) > 16 {
		return current.String()
	}
	for i := len(chain) - 1; i >= 0 && isTrusted(current); i-- {
		addr, err := netip.ParseAddr(strings.TrimSpace(chain[i]))
		if err != nil {
			return peer.Addr().Unmap().String()
		}
		current = addr.Unmap()
	}
	return current.String()
}

// RateLimiter implementations must share state across application replicas.
type RateLimiter interface {
	Allow(context.Context, string, int, time.Duration) (bool, error)
}

// Observer implementations must use bounded labels and exclude customer identifiers.
type Observer interface {
	ObserveRequest(context.Context, string, int, time.Duration)
}
