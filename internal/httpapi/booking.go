package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/mazenessam77/go-airline-booking/internal/auth"
	"github.com/mazenessam77/go-airline-booking/internal/booking"
	"github.com/mazenessam77/go-airline-booking/internal/database"
	"github.com/mazenessam77/go-airline-booking/internal/idempotency"
	"github.com/mazenessam77/go-airline-booking/internal/quote"
)

func domainError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, booking.ErrBookingNotFound), errors.Is(err, booking.ErrPassengerNotFound):
		Error(w, r, 404, "not_found", "Resource not found")
	case errors.Is(err, booking.ErrInvalidHoldRequest), errors.Is(err, quote.ErrInvalid), errors.Is(err, idempotency.ErrKey):
		Error(w, r, 400, "invalid_input", "Invalid request input")
	case errors.Is(err, booking.ErrSeatUnavailable), errors.Is(err, booking.ErrBookingNotHoldable), errors.Is(err, quote.ErrUnavailable):
		Error(w, r, 409, "unavailable", "Resource is not available for this operation")
	case errors.Is(err, idempotency.ErrConflict):
		Error(w, r, 409, "idempotency_conflict", "Idempotency key was used with a different request")
	case database.Temporary(err):
		w.Header().Set("Retry-After", "1")
		Error(w, r, 503, "temporary_conflict", "Please retry with the same idempotency key")
	default:
		Error(w, r, 500, "internal_error", "An internal error occurred")
	}
}

type bookingDTO struct {
	ID            string     `json:"id"`
	PNR           string     `json:"pnr"`
	Status        string     `json:"status"`
	Currency      string     `json:"currency"`
	TotalMinor    int64      `json:"total_minor"`
	Version       int64      `json:"version"`
	HoldExpiresAt *time.Time `json:"hold_expires_at,omitempty"`
}

func bookingResponse(v booking.Summary) bookingDTO {
	if v.HoldExpiresAt != nil {
		t := v.HoldExpiresAt.UTC()
		v.HoldExpiresAt = &t
	}
	return bookingDTO{v.ID, v.PNR, v.Status, v.Currency, v.TotalMinor, v.Version, v.HoldExpiresAt}
}

type passengerDTO struct {
	ID          string `json:"id,omitempty"`
	Type        string `json:"passenger_type"`
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	DateOfBirth string `json:"date_of_birth,omitempty"`
}

func passengerResponse(v booking.Passenger) passengerDTO {
	date := ""
	if v.DateOfBirth != nil {
		date = v.DateOfBirth.Format("2006-01-02")
	}
	return passengerDTO{v.ID, v.Type, v.FirstName, v.LastName, date}
}

func RegisterBookingRoutes(mux *http.ServeMux, a AuthHandler, s *booking.Store, q quote.Store) {
	mux.HandleFunc("GET /v1/bookings", a.Require(func(w http.ResponseWriter, r *http.Request) {
		p, _ := auth.FromContext(r.Context())
		items, err := s.List(r.Context(), p.UserID, r.URL.Query().Get("after_id"), pageLimit(r))
		if err != nil {
			domainError(w, r, err)
			return
		}
		result := make([]bookingDTO, 0, len(items))
		for _, v := range items {
			result = append(result, bookingResponse(v))
		}
		JSON(w, 200, map[string]any{"items": result})
	}))
	mutation := func(next http.HandlerFunc) http.HandlerFunc {
		return a.Require(func(w http.ResponseWriter, r *http.Request) {
			p, _ := auth.FromContext(r.Context())
			if !idempotency.Valid(r.Header.Get("Idempotency-Key")) {
				domainError(w, r, idempotency.ErrKey)
				return
			}
			if !a.limit(w, r, "booking-user:"+p.UserID, 60) {
				return
			}
			next(w, r)
		})
	}
	mux.HandleFunc("POST /v1/quotes", a.Require(func(w http.ResponseWriter, r *http.Request) {
		p, _ := auth.FromContext(r.Context())
		if !a.limit(w, r, "quote-user:"+p.UserID, 30) {
			return
		}
		var dto struct {
			Items []struct {
				FareOfferID    string `json:"fare_offer_id"`
				PassengerCount int    `json:"passenger_count"`
			} `json:"items"`
		}
		if !Decode(w, r, &dto) {
			return
		}
		items := make([]quote.Selection, 0, len(dto.Items))
		for _, v := range dto.Items {
			items = append(items, quote.Selection{FareOfferID: v.FareOfferID, PassengerCount: v.PassengerCount})
		}
		v, err := q.Create(r.Context(), p.UserID, items)
		if err != nil {
			domainError(w, r, err)
			return
		}
		JSON(w, 201, struct {
			ID         string    `json:"id"`
			Currency   string    `json:"currency"`
			TotalMinor int64     `json:"total_minor"`
			ExpiresAt  time.Time `json:"expires_at"`
		}{v.ID, v.Currency, v.TotalMinor, v.ExpiresAt.UTC()})
	}))
	mux.HandleFunc("POST /v1/bookings", mutation(func(w http.ResponseWriter, r *http.Request) {
		var dto struct {
			QuoteID string `json:"quote_id"`
		}
		if !Decode(w, r, &dto) {
			return
		}
		p, _ := auth.FromContext(r.Context())
		v, err := s.Create(r.Context(), p.UserID, dto.QuoteID, r.Header.Get("Idempotency-Key"))
		if err != nil {
			domainError(w, r, err)
			return
		}
		JSON(w, 201, bookingResponse(v))
	}))
	mux.HandleFunc("GET /v1/bookings/{booking_id}", a.Require(func(w http.ResponseWriter, r *http.Request) {
		p, _ := auth.FromContext(r.Context())
		v, err := s.Get(r.Context(), p.UserID, r.PathValue("booking_id"))
		if err != nil {
			domainError(w, r, err)
			return
		}
		type segmentDTO struct {
			ID               string `json:"id"`
			FlightInstanceID string `json:"flight_instance_id"`
			Status           string `json:"status"`
			Cabin            string `json:"cabin"`
		}
		passengers := []passengerDTO{}
		segments := []segmentDTO{}
		for _, p := range v.Passengers {
			passengers = append(passengers, passengerResponse(p))
		}
		for _, seg := range v.Segments {
			segments = append(segments, segmentDTO{seg.ID, seg.FlightInstanceID, seg.Status, seg.Cabin})
		}
		JSON(w, 200, struct {
			bookingDTO
			PassengerCount int            `json:"passenger_count"`
			Passengers     []passengerDTO `json:"passengers"`
			Segments       []segmentDTO   `json:"segments"`
		}{bookingResponse(v.Summary), v.PassengerCount, passengers, segments})
	}))
	for _, route := range []string{"POST /v1/bookings/{booking_id}/passengers", "PATCH /v1/bookings/{booking_id}/passengers/{passenger_id}"} {
		mux.HandleFunc(route, mutation(func(w http.ResponseWriter, r *http.Request) {
			var dto struct {
				Type        string `json:"passenger_type"`
				FirstName   string `json:"first_name"`
				LastName    string `json:"last_name"`
				DateOfBirth string `json:"date_of_birth"`
			}
			if !Decode(w, r, &dto) {
				return
			}
			p, _ := auth.FromContext(r.Context())
			v := booking.Passenger{ID: r.PathValue("passenger_id"), Type: dto.Type, FirstName: dto.FirstName, LastName: dto.LastName}
			if dto.DateOfBirth != "" {
				date, err := time.Parse("2006-01-02", dto.DateOfBirth)
				if err != nil {
					domainError(w, r, booking.ErrInvalidHoldRequest)
					return
				}
				v.DateOfBirth = &date
			}
			v, err := s.SavePassenger(r.Context(), p.UserID, r.PathValue("booking_id"), r.Header.Get("Idempotency-Key"), v)
			if err != nil {
				domainError(w, r, err)
				return
			}
			JSON(w, 200, passengerResponse(v))
		}))
	}
	mux.HandleFunc("POST /v1/bookings/{booking_id}/seat-holds", mutation(func(w http.ResponseWriter, r *http.Request) {
		var dto struct {
			SegmentID string `json:"booking_segment_id"`
			FlightID  string `json:"flight_instance_id"`
			Seats     []struct {
				PassengerID string `json:"passenger_id"`
				SeatID      string `json:"flight_seat_id"`
			} `json:"seats"`
		}
		if !Decode(w, r, &dto) {
			return
		}
		p, _ := auth.FromContext(r.Context())
		seats := make([]booking.SeatSelection, 0, len(dto.Seats))
		for _, v := range dto.Seats {
			seats = append(seats, booking.SeatSelection{PassengerID: v.PassengerID, FlightSeatID: v.SeatID})
		}
		v, err := s.HoldSeats(r.Context(), booking.HoldSeatsParams{UserID: p.UserID, BookingID: r.PathValue("booking_id"), BookingSegmentID: dto.SegmentID, FlightInstanceID: dto.FlightID, Seats: seats, IdempotencyKey: r.Header.Get("Idempotency-Key")})
		if err != nil {
			domainError(w, r, err)
			return
		}
		JSON(w, 200, struct {
			BookingID string    `json:"booking_id"`
			SeatIDs   []string  `json:"seat_ids"`
			ExpiresAt time.Time `json:"expires_at"`
		}{v.BookingID, v.SeatIDs, v.ExpiresAt.UTC()})
	}))
	for _, cancel := range []bool{false, true} {
		route := "DELETE /v1/bookings/{booking_id}/seat-holds"
		if cancel {
			route = "POST /v1/bookings/{booking_id}/cancel"
		}
		mux.HandleFunc(route, mutation(func(w http.ResponseWriter, r *http.Request) {
			p, _ := auth.FromContext(r.Context())
			if err := s.Release(r.Context(), p.UserID, r.PathValue("booking_id"), r.Header.Get("Idempotency-Key"), cancel); err != nil {
				domainError(w, r, err)
				return
			}
			w.WriteHeader(204)
		}))
	}
	mux.HandleFunc("GET /v1/bookings/{booking_id}/seats", a.Require(func(w http.ResponseWriter, r *http.Request) {
		p, _ := auth.FromContext(r.Context())
		seats, err := s.Seats(r.Context(), p.UserID, r.PathValue("booking_id"))
		if err != nil {
			domainError(w, r, err)
			return
		}
		type dto struct {
			SeatID      string `json:"seat_id"`
			PassengerID string `json:"passenger_id"`
			SegmentID   string `json:"segment_id"`
			Status      string `json:"status"`
		}
		result := []dto{}
		for _, v := range seats {
			result = append(result, dto{v.SeatID, v.PassengerID, v.SegmentID, v.Status})
		}
		JSON(w, 200, map[string]any{"items": result})
	}))
}
