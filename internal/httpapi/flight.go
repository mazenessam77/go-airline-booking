package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/mazenessam77/go-airline-booking/internal/flight"
)

type flightDTO struct {
	ID          string    `json:"id"`
	Airline     string    `json:"airline"`
	Number      string    `json:"number"`
	Origin      string    `json:"origin"`
	Destination string    `json:"destination"`
	Status      string    `json:"status"`
	Departure   time.Time `json:"departure"`
	Arrival     time.Time `json:"arrival"`
}

func flightResponse(f flight.Flight) flightDTO {
	return flightDTO{f.ID, f.Airline, f.Number, f.Origin, f.Destination, f.Status, f.Departure.UTC(), f.Arrival.UTC()}
}
func pageLimit(r *http.Request) int {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return 50
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return n
}
func flightError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, flight.ErrInvalid) {
		Error(w, r, 400, "invalid_input", "Invalid flight query")
	} else if errors.Is(err, flight.ErrNotFound) {
		Error(w, r, 404, "not_found", "Resource not found")
	} else {
		Error(w, r, 500, "internal_error", "An internal error occurred")
	}
}
func RegisterFlightRoutes(mux *http.ServeMux, s flight.Store) {
	mux.HandleFunc("GET /v1/flights", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		after := time.Time{}
		if raw := q.Get("after_departure"); raw != "" {
			var err error
			after, err = time.Parse(time.RFC3339Nano, raw)
			if err != nil {
				flightError(w, r, flight.ErrInvalid)
				return
			}
		}
		items, err := s.Search(r.Context(), flight.Search{Origin: q.Get("origin"), Destination: q.Get("destination"), Date: q.Get("date"), AfterID: q.Get("after_id"), AfterDeparture: after, Limit: pageLimit(r)})
		if err != nil {
			flightError(w, r, err)
			return
		}
		result := make([]flightDTO, 0, len(items))
		for _, f := range items {
			result = append(result, flightResponse(f))
		}
		JSON(w, 200, map[string]any{"items": result})
	})
	mux.HandleFunc("GET /v1/flight-instances/{id}", func(w http.ResponseWriter, r *http.Request) {
		f, err := s.Get(r.Context(), r.PathValue("id"))
		if err != nil {
			flightError(w, r, err)
			return
		}
		fares, err := s.Fares(r.Context(), f.ID)
		if err != nil {
			flightError(w, r, err)
			return
		}
		type fareDTO struct {
			ID         string `json:"id"`
			Code       string `json:"code"`
			Cabin      string `json:"cabin"`
			Currency   string `json:"currency"`
			PriceMinor int64  `json:"price_minor"`
			TaxesMinor int64  `json:"taxes_minor"`
			Refundable bool   `json:"refundable"`
		}
		result := make([]fareDTO, 0, len(fares))
		for _, v := range fares {
			result = append(result, fareDTO{v.ID, v.Code, v.Cabin, v.Currency, v.PriceMinor, v.TaxesMinor, v.Refundable})
		}
		JSON(w, 200, struct {
			flightDTO
			Fares []fareDTO `json:"fares"`
		}{flightResponse(f), result})
	})
	mux.HandleFunc("GET /v1/flight-instances/{id}/seats", func(w http.ResponseWriter, r *http.Request) {
		items, err := s.Seats(r.Context(), r.PathValue("id"), r.URL.Query().Get("after_id"), pageLimit(r))
		if err != nil {
			flightError(w, r, err)
			return
		}
		type dto struct {
			ID           string `json:"id"`
			Number       string `json:"number"`
			Cabin        string `json:"cabin"`
			Availability string `json:"availability"`
		}
		result := make([]dto, 0, len(items))
		for _, v := range items {
			result = append(result, dto{v.ID, v.Number, v.Cabin, v.Availability})
		}
		JSON(w, 200, map[string]any{"items": result})
	})
}
