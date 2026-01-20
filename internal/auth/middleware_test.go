package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sha1n/mcp-acdc-server-go/internal/config"
)

func TestBasicAuth(t *testing.T) {
	settings := config.BasicAuthSettings{
		Username: "user",
		Password: "password",
	}
	middleware := basicAuthMiddleware(settings)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Test valid credentials
	req := httptest.NewRequest("GET", "/", nil)
	req.SetBasicAuth("user", "password")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	// Test invalid credentials
	req = httptest.NewRequest("GET", "/", nil)
	req.SetBasicAuth("user", "wrong")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", w.Code)
	}

	// Test missing credentials
	req = httptest.NewRequest("GET", "/", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", w.Code)
	}
}

func TestAPIKeyAuth(t *testing.T) {
	apiKeys := []string{"key-1", "key-2"}
	middleware := apiKeyMiddleware(apiKeys)
	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Test valid header (Key 1)
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-API-Key", "key-1")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 for key-1, got %d", w.Code)
	}

	// Test valid header (Key 2)
	req = httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-API-Key", "key-2")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 for key-2, got %d", w.Code)
	}

	// Test invalid key
	req = httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-API-Key", "wrong")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401 for wrong key, got %d", w.Code)
	}

	// Test empty key
	req = httptest.NewRequest("GET", "/", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401 for missing key, got %d", w.Code)
	}
}

func TestNewMiddleware(t *testing.T) {
	// Test None
	mw, err := NewMiddleware(config.AuthSettings{Type: config.AuthTypeNone})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	// Test Basic
	mw, err = NewMiddleware(config.AuthSettings{
		Type: config.AuthTypeBasic,
		Basic: config.BasicAuthSettings{
			Username: "u",
			Password: "p",
		},
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	// Verify it requires auth
	req = httptest.NewRequest("GET", "/", nil)
	w = httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401 for basic auth without creds")
	}

	// Test Basic with empty username
	_, err = NewMiddleware(config.AuthSettings{
		Type: config.AuthTypeBasic,
		Basic: config.BasicAuthSettings{
			Username: "",
			Password: "p",
		},
	})
	if err == nil {
		t.Error("Expected error for basic auth with empty username")
	}

	// Test Basic with empty password
	_, err = NewMiddleware(config.AuthSettings{
		Type: config.AuthTypeBasic,
		Basic: config.BasicAuthSettings{
			Username: "u",
			Password: "",
		},
	})
	if err == nil {
		t.Error("Expected error for basic auth with empty password")
	}

	// Test APIKey with valid keys
	mw, err = NewMiddleware(config.AuthSettings{
		Type:    config.AuthTypeAPIKey,
		APIKeys: []string{"valid-key"},
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	// Verify it requires auth
	req = httptest.NewRequest("GET", "/api/test", nil)
	w = httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401 for apikey auth without key, got %d", w.Code)
	}
	// Verify valid key works
	req = httptest.NewRequest("GET", "/api/test", nil)
	req.Header.Set("X-API-Key", "valid-key")
	w = httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 for apikey auth with valid key, got %d", w.Code)
	}

	// Test APIKey with empty list
	_, err = NewMiddleware(config.AuthSettings{
		Type:    config.AuthTypeAPIKey,
		APIKeys: []string{},
	})
	if err == nil {
		t.Error("Expected error for apikey auth with empty list")
	}

	// Test Unknown
	_, err = NewMiddleware(config.AuthSettings{Type: "unknown"})
	if err == nil {
		t.Error("Expected error for unknown auth type")
	}
}

func TestPathExclusions(t *testing.T) {
	// Create a basic auth middleware that requires auth
	mw, err := NewMiddleware(config.AuthSettings{
		Type: config.AuthTypeBasic,
		Basic: config.BasicAuthSettings{
			Username: "user",
			Password: "pass",
		},
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Test that /health is accessible without auth
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("/health should be accessible without auth, got %d", w.Code)
	}

	// Test that /ready is accessible without auth
	req = httptest.NewRequest("GET", "/ready", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("/ready should be accessible without auth, got %d", w.Code)
	}

	// Test that other paths still require auth
	req = httptest.NewRequest("GET", "/api/data", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("/api/data should require auth, got %d", w.Code)
	}

	// Test that auth still works for non-excluded paths
	req = httptest.NewRequest("GET", "/api/data", nil)
	req.SetBasicAuth("user", "pass")
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("/api/data with valid auth should succeed, got %d", w.Code)
	}
}
