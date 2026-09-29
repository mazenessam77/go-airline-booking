package observability

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestMiddlewareLabelsByRoutePattern(t *testing.T) {
	p := NewPrometheus()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/items/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	route := func(r *http.Request) string {
		if _, pattern := mux.Handler(r); pattern != "" {
			return pattern
		}
		return "unmatched"
	}
	h := p.Middleware(mux, route)
	for _, path := range []string{"/v1/items/a", "/v1/items/b", "/nope"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}
	if got := testutil.ToFloat64(p.requests.WithLabelValues("GET", "GET /v1/items/{id}", "418")); got != 2 {
		t.Fatalf("route counter = %v, want 2", got)
	}
	if got := testutil.ToFloat64(p.requests.WithLabelValues("GET", "unmatched", "404")); got != 1 {
		t.Fatalf("unmatched counter = %v, want 1", got)
	}

	rec := httptest.NewRecorder()
	p.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body, _ := io.ReadAll(rec.Body)
	for _, want := range []string{"go_goroutines", "http_request_duration_seconds_bucket", `route="GET /v1/items/{id}"`} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("metrics output missing %q", want)
		}
	}
}
