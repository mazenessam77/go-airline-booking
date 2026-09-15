package httpapi

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestStrictDecode(t *testing.T) {
	for _, tc := range []struct {
		body, content string
		want          int
	}{
		{`{"name":"ok"}`, "application/json", 200},
		{`{"name":"ok","role":"ADMIN"}`, "application/json", 400},
		{`{} {}`, "application/json", 400},
		{`{}`, "text/plain", 415},
		{``, "application/json", 400},
		{`{"name":"` + strings.Repeat("x", 100) + `"}`, "application/json", 413},
	} {
		h := New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var dto struct {
				Name string `json:"name"`
			}
			if Decode(w, r, &dto) {
				JSON(w, 200, dto)
			}
		}), Options{BodyLimit: 64, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
		r := httptest.NewRequest("POST", "/", strings.NewReader(tc.body))
		r.Header.Set("Content-Type", tc.content)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("body=%q got=%d want=%d", tc.body, w.Code, tc.want)
		}
	}
}

func TestMiddlewareRedactionAndRecovery(t *testing.T) {
	var logs bytes.Buffer
	h := New(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("private panic secret") }), Options{Logger: slog.New(slog.NewJSONHandler(&logs, nil))})
	r := httptest.NewRequest("GET", "/private-token?password=secret", nil)
	r.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 500 || w.Header().Get("X-Request-ID") == "" {
		t.Fatal("missing recovery or request ID")
	}
	if strings.Contains(logs.String()+w.Body.String(), "secret") || strings.Contains(logs.String(), "private") {
		t.Fatal("sensitive data leaked")
	}
}

func TestTimeout(t *testing.T) {
	h := New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }), Options{Timeout: time.Millisecond, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 503 {
		t.Fatalf("got %d", w.Code)
	}
}

func TestTrustedProxy(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "192.0.2.1:123"
	r.Header.Set("X-Forwarded-For", "203.0.113.9, 198.51.100.5")
	if got := ClientIP(r, nil); got != "192.0.2.1" {
		t.Fatal(got)
	}
	if got := ClientIP(r, []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}); got != "198.51.100.5" {
		t.Fatal(got)
	}
}

func TestCORSAllowsOnlySameOrConfiguredOrigins(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	h := New(next, Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), AllowedOrigins: []string{"https://mobile.example.test"}})
	for _, tc := range []struct {
		origin string
		want   int
	}{
		{"http://airline.local", http.StatusNoContent},
		{"https://mobile.example.test", http.StatusNoContent},
		{"https://evil.example.test", http.StatusForbidden},
	} {
		r := httptest.NewRequest(http.MethodPost, "http://airline.local/v1/test", nil)
		r.Header.Set("Origin", tc.origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("origin=%q got=%d want=%d", tc.origin, w.Code, tc.want)
		}
		if tc.want < 400 && w.Header().Get("Access-Control-Allow-Origin") != tc.origin {
			t.Fatalf("origin=%q missing explicit allow header", tc.origin)
		}
	}
}
