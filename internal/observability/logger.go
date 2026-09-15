package observability

import (
	"io"
	"log/slog"
	"strings"
)

func NewLogger(output io.Writer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(output, &slog.HandlerOptions{ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
		if a.Key == slog.TimeKey {
			return slog.Time(a.Key, a.Value.Time().UTC())
		}
		switch strings.ToLower(a.Key) {
		case "authorization", "cookie", "password", "password_hash", "token", "access_token", "refresh_token", "manage_token", "travel_document", "payment_data", "error":
			return slog.String(a.Key, "[REDACTED]")
		}
		return a
	}}))
}
