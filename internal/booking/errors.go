package booking

import "errors"

var (
	ErrInvalidHoldRequest = errors.New(
		"invalid seat hold request",
	)

	ErrBookingNotFound = errors.New(
		"booking or booking segment not found",
	)

	ErrBookingNotHoldable = errors.New(
		"booking cannot hold seats",
	)

	ErrPassengerNotFound = errors.New(
		"passenger does not belong to booking",
	)

	ErrSeatUnavailable = errors.New(
		"one or more seats are unavailable",
	)

	ErrInventoryConflict = errors.New(
		"seat inventory is inconsistent",
	)
)
