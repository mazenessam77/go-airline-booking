package booking

import (
	"errors"
	"testing"

	"github.com/mazenessam77/go-airline-booking/internal/testsupport"
)

func TestListAndDetailsRemainOwnerScoped(t *testing.T) {
	ctx, pool := testsupport.Open(t)
	fixture := testsupport.Seed(t, ctx, pool)
	store := NewStore(pool)

	items, err := store.List(ctx, fixture.User, "", 50)
	if err != nil || len(items) != 1 || items[0].ID != fixture.Booking {
		t.Fatalf("owner list mismatch: items=%+v err=%v", items, err)
	}
	items, err = store.List(ctx, fixture.OtherUser, "", 50)
	if err != nil || len(items) != 0 {
		t.Fatalf("another user can observe bookings: items=%+v err=%v", items, err)
	}
	items, err = store.List(ctx, fixture.User, fixture.Booking, 50)
	if err != nil || len(items) != 0 {
		t.Fatalf("cursor page mismatch: items=%+v err=%v", items, err)
	}
	if _, err = store.List(ctx, fixture.User, "not-a-uuid", 50); !errors.Is(err, ErrInvalidHoldRequest) {
		t.Fatalf("invalid cursor error=%v", err)
	}

	details, err := store.Get(ctx, fixture.User, fixture.Booking)
	if err != nil || details.PassengerCount != 1 || len(details.Segments) != 1 || details.Segments[0].Cabin != "ECONOMY" {
		t.Fatalf("booking details mismatch: details=%+v err=%v", details, err)
	}
	if _, err = store.Get(ctx, fixture.OtherUser, fixture.Booking); !errors.Is(err, ErrBookingNotFound) {
		t.Fatalf("wrong owner error=%v", err)
	}
}
