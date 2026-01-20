package auth

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
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
		return apiKeyMiddleware(settings.APIKey), nil
	case "oidc":
		return oidcMiddleware(settings.OIDC)
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

func apiKeyMiddleware(apiKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get("X-API-Key")
			if key == "" {
				key = r.URL.Query().Get("api_key")
			}

			if subtle.ConstantTimeCompare([]byte(key), []byte(apiKey)) != 1 {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func oidcMiddleware(settings config.OIDCSettings) (func(http.Handler) http.Handler, error) {
	provider, err := oidc.NewProvider(context.Background(), settings.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("failed to create OIDC provider: %w", err)
	}

	verifier := provider.Verifier(&oidc.Config{
		ClientID: settings.ClientID,
	})

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			var tokenString string

			if strings.HasPrefix(authHeader, "Bearer ") {
				tokenString = strings.TrimPrefix(authHeader, "Bearer ")
			} else {
				tokenString = r.URL.Query().Get("token")
			}

			if tokenString == "" {
				http.Error(w, "Unauthorized: missing token", http.StatusUnauthorized)
				return
			}

			_, err := verifier.Verify(r.Context(), tokenString)
			if err != nil {
				http.Error(w, fmt.Sprintf("Unauthorized: invalid token: %v", err), http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}, nil
}
