// Package webui serves the small, same-origin booking application.
package webui

import (
	"embed"
	"net/http"
)

//go:embed assets/*
var assets embed.FS

func Handler(api http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, contentType := "", ""
		switch r.URL.Path {
		case "/":
			name = "index.html"
			contentType = "text/html; charset=utf-8"
		case "/assets/app.js":
			name = "app.js"
			contentType = "text/javascript; charset=utf-8"
		case "/assets/style.css":
			name = "style.css"
			contentType = "text/css; charset=utf-8"
		}
		if name == "" || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
			api.ServeHTTP(w, r)
			return
		}
		body, err := assets.ReadFile("assets/" + name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		if r.Method == http.MethodGet {
			_, _ = w.Write(body)
		}
	})
}
