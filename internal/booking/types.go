package booking

import "time"

type SeatSelection struct {
	PassengerID  string
	FlightSeatID string
}

type HoldSeatsParams struct {
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
