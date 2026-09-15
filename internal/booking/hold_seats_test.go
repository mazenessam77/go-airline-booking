package booking

import "testing"

func TestNormalizeHoldParams(t *testing.T) {
	const id = "00000000-0000-4000-8000-000000000001"
	valid := HoldSeatsParams{UserID: id, BookingID: id, BookingSegmentID: id, FlightInstanceID: id,
		Seats: []SeatSelection{{PassengerID: id, FlightSeatID: id}}}
	for _, tc := range []struct {
		name   string
		change func(*HoldSeatsParams)
	}{
		{"missing owner", func(p *HoldSeatsParams) { p.UserID = "" }},
		{"nil owner", func(p *HoldSeatsParams) { p.UserID = "00000000-0000-0000-0000-000000000000" }},
		{"invalid booking", func(p *HoldSeatsParams) { p.BookingID = "invalid" }},
		{"empty seats", func(p *HoldSeatsParams) { p.Seats = nil }},
		{"duplicate selection", func(p *HoldSeatsParams) { p.Seats = append(p.Seats, p.Seats[0]) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := valid
			tc.change(&p)
			if _, err := normalizeHoldParams(p); err != ErrInvalidHoldRequest {
				t.Fatalf("got %v", err)
			}
		})
	}
	if _, err := normalizeHoldParams(valid); err != nil {
		t.Fatal(err)
	}
}
