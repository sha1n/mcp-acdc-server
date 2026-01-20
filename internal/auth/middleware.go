package auth

import (
	"crypto/subtle"
	"fmt"
	"net/http"

	"github.com/sha1n/mcp-acdc-server-go/internal/config"
)

// excludedPaths are paths that bypass authentication (e.g., health checks)
var excludedPaths = map[string]bool{
	"/health": true,
	"/ready":  true,
}

// isExcludedPath checks if the request path should bypass authentication
func isExcludedPath(path string) bool {
	return excludedPaths[path]
}

// NewMiddleware creates a new authentication middleware based on settings
func NewMiddleware(settings config.AuthSettings) (func(http.Handler) http.Handler, error) {
	switch settings.Type {
	case config.AuthTypeNone, "":
		return func(next http.Handler) http.Handler {
			return next
		}, nil
	case config.AuthTypeBasic:
		if settings.Basic.Username == "" || settings.Basic.Password == "" {
			return nil, fmt.Errorf("basic auth requires non-empty username and password")
		}
		return withExclusions(basicAuthMiddleware(settings.Basic)), nil
	case config.AuthTypeAPIKey:
		if len(settings.APIKeys) == 0 {
			return nil, fmt.Errorf("apikey auth requires at least one API key")
		}
		return withExclusions(apiKeyMiddleware(settings.APIKeys)), nil
	default:
		return nil, fmt.Errorf("unknown auth type: %s", settings.Type)
	}
}

// withExclusions wraps an auth middleware to skip auth for excluded paths
func withExclusions(authMiddleware func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		authedHandler := authMiddleware(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isExcludedPath(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}
			authedHandler.ServeHTTP(w, r)
		})
	}
}

func basicAuthMiddleware(settings config.BasicAuthSettings) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, pass, ok := r.BasicAuth()
			userMatch := subtle.ConstantTimeCompare([]byte(user), []byte(settings.Username)) == 1
			passMatch := subtle.ConstantTimeCompare([]byte(pass), []byte(settings.Password)) == 1
			if !ok || !userMatch || !passMatch {
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
