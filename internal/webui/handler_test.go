package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesAssetsAndDelegatesAPI(t *testing.T) {
	api := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTeapot)
	})
	h := Handler(api)

	for _, tc := range []struct {
		path, contentType, marker string
	}{
		{"/", "text/html", "search-form"},
		{"/assets/app.js", "text/javascript", "/v1/flights"},
		{"/assets/style.css", "text/css", ".booking-panel"},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), tc.contentType) || !strings.Contains(w.Body.String(), tc.marker) {
			t.Fatalf("asset %s was not served correctly", tc.path)
		}
		if !strings.Contains(w.Header().Get("Content-Security-Policy"), "connect-src 'self'") {
			t.Fatal("static application CSP is missing")
		}
	}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/ready", nil))
	if w.Code != http.StatusTeapot {
		t.Fatalf("API route was not delegated: %d", w.Code)
	}
}
