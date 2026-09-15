package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mazenessam77/go-airline-booking/internal/auth"
	"github.com/mazenessam77/go-airline-booking/internal/booking"
	"github.com/mazenessam77/go-airline-booking/internal/quote"
	"github.com/mazenessam77/go-airline-booking/internal/testsupport"
)

func TestBookingHTTPAuthorization(t *testing.T) {
	ctx, pool := testsupport.Open(t)
	f := testsupport.Seed(t, ctx, pool)
	s := auth.NewStore(pool)
	hash, err := auth.HashPassword("an integration password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE users SET password_hash=$1 WHERE id=ANY($2::uuid[])`, hash, []string{f.User, f.OtherUser}); err != nil {
		t.Fatal(err)
	}
	owner, err := s.Login(ctx, "scenario@example.test", "an integration password")
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.Login(ctx, "other@example.test", "an integration password")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	a := AuthHandler{Store: s, Limiter: PostgresRateLimiter{Pool: pool}}
	a.RegisterRoutes(mux)
	RegisterBookingRoutes(mux, a, booking.NewStore(pool), quote.Store{Pool: pool})
	h := New(mux, Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	for _, tc := range []struct {
		token string
		want  int
	}{{"", 401}, {other.AccessToken, 404}, {owner.AccessToken, 200}} {
		r := httptest.NewRequest("GET", "/v1/bookings/"+f.Booking, nil)
		if tc.token != "" {
			r.Header.Set("Authorization", "Bearer "+tc.token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("got %d want %d", w.Code, tc.want)
		}
	}
	r := httptest.NewRequest("POST", "/v1/bookings/"+f.Booking+"/seat-holds", strings.NewReader(`{"user_id":"`+f.OtherUser+`"}`))
	r.Header.Set("Authorization", "Bearer "+owner.AccessToken)
	r.Header.Set("Idempotency-Key", "http-hold-test-0001")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatalf("client user ID accepted: %d %s", w.Code, w.Body.String())
	}
	r = httptest.NewRequest("POST", "/v1/auth/login", strings.NewReader(`{"email":"absent@example.test","password":"incorrect"}`))
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("auth rate limiter query: %d %s", w.Code, w.Body.String())
	}
}
