package auth

import (
	"crypto/subtle"
	"log"
	"net/http"
	"strings"

	"repoxy/internal/config"
)

// Middleware wraps an HTTP handler with authentication
func Middleware(cfg *config.AuthConfig, next http.Handler) http.Handler {
	if !cfg.Enabled {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip authentication for health/metrics endpoints
		if r.URL.Path == "/_healthz" || r.URL.Path == "/_metrics" {
			next.ServeHTTP(w, r)
			return
		}

		var authenticated bool

		switch strings.ToLower(cfg.Type) {
		case "basic":
			authenticated = checkBasicAuth(r, cfg.Users)
		case "bearer", "token":
			authenticated = checkBearerAuth(r, cfg.Tokens)
		default:
			log.Printf("Warning: unknown auth type: %s", cfg.Type)
			authenticated = false
		}

		if !authenticated {
			w.Header().Set("WWW-Authenticate", `Basic realm="EdgeCache"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// checkBasicAuth validates HTTP Basic Authentication
func checkBasicAuth(r *http.Request, users map[string]string) bool {
	username, password, ok := r.BasicAuth()
	if !ok {
		return false
	}

	expectedPassword, exists := users[username]
	if !exists {
		return false
	}

	// Use constant-time comparison to prevent timing attacks
	return subtle.ConstantTimeCompare([]byte(password), []byte(expectedPassword)) == 1
}

// checkBearerAuth validates Bearer token authentication
func checkBearerAuth(r *http.Request, validTokens []string) bool {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return false
	}

	// Check for Bearer token
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 {
		return false
	}

	scheme := strings.ToLower(parts[0])
	token := parts[1]

	if scheme != "bearer" {
		return false
	}

	// Check if token is in the valid list
	for _, validToken := range validTokens {
		if subtle.ConstantTimeCompare([]byte(token), []byte(validToken)) == 1 {
			return true
		}
	}

	return false
}
