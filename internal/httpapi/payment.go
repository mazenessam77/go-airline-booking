package httpapi

import (
	"bytes"
	"io"
	"net/http"
	"time"

	"github.com/mazenessam77/go-airline-booking/internal/auth"
	"github.com/mazenessam77/go-airline-booking/internal/booking"
	"github.com/mazenessam77/go-airline-booking/internal/payment"
)

func RegisterPaymentRoutes(mux *http.ServeMux, a AuthHandler, b *booking.Store, s payment.Store, secret []byte) {
	mux.HandleFunc("POST /v1/bookings/{booking_id}/refunds", a.Require(func(w http.ResponseWriter, r *http.Request) {
		p, _ := auth.FromContext(r.Context())
		if _, err := b.Get(r.Context(), p.UserID, r.PathValue("booking_id")); err != nil {
			domainError(w, r, err)
			return
		}
		if s.Provider == nil {
			Error(w, r, 503, "payments_disabled", "Payment processing is not configured")
			return
		}
		var dto struct{}
		if !Decode(w, r, &dto) {
			return
		}
		if !a.limit(w, r, "payment-user:"+p.UserID, 10) {
			return
		}
		id, err := s.RequestRefund(r.Context(), p.UserID, r.PathValue("booking_id"), r.Header.Get("Idempotency-Key"))
		if err != nil {
			domainError(w, r, err)
			return
		}
		JSON(w, 202, map[string]string{"id": id, "status": "PENDING"})
	}))
	mux.HandleFunc("POST /v1/bookings/{booking_id}/payments", a.Require(func(w http.ResponseWriter, r *http.Request) {
		p, _ := auth.FromContext(r.Context())
		if _, err := b.Get(r.Context(), p.UserID, r.PathValue("booking_id")); err != nil {
			domainError(w, r, err)
			return
		}
		if s.Provider == nil {
			Error(w, r, 503, "payments_disabled", "Payment processing is not configured")
			return
		}
		var dto struct{}
		if !Decode(w, r, &dto) {
			return
		}
		if !a.limit(w, r, "payment-user:"+p.UserID, 10) {
			return
		}
		v, err := s.Start(r.Context(), p.UserID, r.PathValue("booking_id"), r.Header.Get("Idempotency-Key"))
		if err != nil {
			domainError(w, r, err)
			return
		}
		JSON(w, 202, struct {
			ID          string `json:"id"`
			Status      string `json:"status"`
			AmountMinor int64  `json:"amount_minor"`
			Currency    string `json:"currency"`
		}{v.ID, v.Status, v.AmountMinor, v.Currency})
	}))
	mux.HandleFunc("POST /v1/payment-webhooks", func(w http.ResponseWriter, r *http.Request) {
		if len(secret) < 32 {
			Error(w, r, 503, "payments_disabled", "Payment processing is not configured")
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			Error(w, r, 400, "invalid_input", "Invalid webhook")
			return
		}
		if !payment.VerifySignature(secret, body, r.Header.Get("X-Webhook-Timestamp"), r.Header.Get("X-Webhook-Signature"), time.Now()) {
			Error(w, r, 401, "invalid_signature", "Invalid webhook signature")
			return
		}
		var dto struct {
			EventID   string `json:"event_id"`
			PaymentID string `json:"payment_id"`
			Status    string `json:"status"`
			Reference string `json:"reference"`
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		if !Decode(w, r, &dto) {
			return
		}
		if err = s.Apply(r.Context(), "configured", dto.EventID, dto.PaymentID, payment.Result{Reference: dto.Reference, Status: dto.Status}); err != nil {
			domainError(w, r, err)
			return
		}
		w.WriteHeader(204)
	})
}
