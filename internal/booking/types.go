package booking

import "time"

type SeatSelection struct {
	PassengerID  string
	FlightSeatID string
}

type HoldSeatsParams struct {
	// UserID must come from a verified authentication principal.
	UserID           string
	IdempotencyKey   string
	BookingID        string
	BookingSegmentID string
	FlightInstanceID string
	Seats            []SeatSelection
}

type SeatHold struct {
	BookingID string
	SeatIDs   []string
	ExpiresAt time.Time
}
