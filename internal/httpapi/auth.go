package httpapi

import (
	"errors"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/mazenessam77/go-airline-booking/internal/auth"
)

type AuthHandler struct {
	Store          *auth.Store
	Limiter        RateLimiter
	TrustedProxies []netip.Prefix
}

func (h AuthHandler) Require(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			Error(w, r, 401, "unauthenticated", "Authentication required")
			return
		}
		p, err := h.Store.Authenticate(r.Context(), strings.TrimPrefix(header, "Bearer "))
		if err != nil {
			h.failure(w, r, err)
			return
		}
		next(w, r.WithContext(auth.WithPrincipal(r.Context(), p)))
	}
}

func (h AuthHandler) Roles(next http.HandlerFunc, roles ...string) http.HandlerFunc {
	return h.Require(func(w http.ResponseWriter, r *http.Request) {
		p, _ := auth.FromContext(r.Context())
		if !p.HasRole(roles...) {
			Error(w, r, 403, "forbidden", "Operation is not allowed")
			return
		}
		next(w, r)
	})
}

func (h AuthHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/auth/register", h.limited(func(w http.ResponseWriter, r *http.Request) {
		var dto struct {
			Email     string `json:"email"`
			Password  string `json:"password"`
			FirstName string `json:"first_name"`
			LastName  string `json:"last_name"`
		}
		if !Decode(w, r, &dto) {
			return
		}
		if err := h.Store.Register(r.Context(), dto.Email, dto.Password, dto.FirstName, dto.LastName); err != nil {
			h.failure(w, r, err)
			return
		}
		JSON(w, 202, map[string]string{"status": "accepted"})
	}))
	mux.HandleFunc("POST /v1/auth/login", h.limited(func(w http.ResponseWriter, r *http.Request) {
		var dto struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if !Decode(w, r, &dto) {
			return
		}
		if !h.accountLimit(w, r, dto.Email) {
			return
		}
		tokens, err := h.Store.Login(r.Context(), dto.Email, dto.Password)
		if err != nil {
			h.failure(w, r, err)
			return
		}
		writeTokens(w, tokens)
	}))
	mux.HandleFunc("POST /v1/auth/refresh", h.limited(func(w http.ResponseWriter, r *http.Request) {
		var dto struct {
			RefreshToken string `json:"refresh_token"`
		}
		if !Decode(w, r, &dto) {
			return
		}
		tokens, err := h.Store.Refresh(r.Context(), dto.RefreshToken)
		if err != nil {
			h.failure(w, r, err)
			return
		}
		writeTokens(w, tokens)
	}))
	for _, all := range []bool{false, true} {
		route := "POST /v1/auth/logout"
		if all {
			route = "POST /v1/auth/logout-all"
		}
		mux.HandleFunc(route, h.Require(func(w http.ResponseWriter, r *http.Request) {
			p, _ := auth.FromContext(r.Context())
			if err := h.Store.Logout(r.Context(), p, all); err != nil {
				h.failure(w, r, err)
				return
			}
			w.WriteHeader(204)
		}))
	}
}

func writeTokens(w http.ResponseWriter, t auth.Tokens) {
	JSON(w, 200, struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
	}{t.AccessToken, t.RefreshToken, "Bearer", t.ExpiresIn})
}

func (h AuthHandler) limited(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.limit(w, r, "auth-ip:"+ClientIP(r, h.TrustedProxies), 30) {
			next(w, r)
		}
	}
}
func (h AuthHandler) accountLimit(w http.ResponseWriter, r *http.Request, email string) bool {
	return h.limit(w, r, "auth-account:"+strings.ToLower(strings.TrimSpace(email)), 10)
}
func (h AuthHandler) limit(w http.ResponseWriter, r *http.Request, key string, count int) bool {
	if h.Limiter == nil {
		Error(w, r, 503, "unavailable", "Service temporarily unavailable")
		return false
	}
	allowed, err := h.Limiter.Allow(r.Context(), key, count, time.Minute)
	if err != nil {
		Error(w, r, 503, "unavailable", "Service temporarily unavailable")
		return false
	}
	if !allowed {
		w.Header().Set("Retry-After", "60")
		Error(w, r, 429, "rate_limited", "Too many requests")
		return false
	}
	return true
}
func (h AuthHandler) failure(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, auth.ErrUnauthorized):
		Error(w, r, 401, "unauthenticated", "Invalid credentials")
	case errors.Is(err, auth.ErrInvalidInput):
		Error(w, r, 400, "invalid_input", "Invalid authentication input")
	case errors.Is(err, auth.ErrBusy):
		Error(w, r, 503, "unavailable", "Service temporarily unavailable")
	default:
		Error(w, r, 500, "internal_error", "An internal error occurred")
	}
}
