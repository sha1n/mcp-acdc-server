package auth

import (
	"crypto/subtle"
	"fmt"
	"net/http"

	"github.com/sha1n/mcp-acdc-server-go/internal/config"
)

// NewMiddleware creates a new authentication middleware based on settings
func NewMiddleware(settings config.AuthSettings) (func(http.Handler) http.Handler, error) {
	switch settings.Type {
	case "none", "":
		return func(next http.Handler) http.Handler {
			return next
		}, nil
	case "basic":
		return basicAuthMiddleware(settings.Basic), nil
	case "apikey":
		return apiKeyMiddleware(settings.APIKeys), nil
	default:
		return nil, fmt.Errorf("unknown auth type: %s", settings.Type)
	}
}

func basicAuthMiddleware(settings config.BasicAuthSettings) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, pass, ok := r.BasicAuth()
			if !ok || subtle.ConstantTimeCompare([]byte(user), []byte(settings.Username)) != 1 || subtle.ConstantTimeCompare([]byte(pass), []byte(settings.Password)) != 1 {
				w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func apiKeyMiddleware(apiKeys []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get("X-API-Key")
			if key == "" {
				key = r.URL.Query().Get("api_key")
			}

			if key == "" {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			valid := false
			for _, validKey := range apiKeys {
				if subtle.ConstantTimeCompare([]byte(key), []byte(validKey)) == 1 {
					valid = true
					break
				}
			}

			if !valid {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
