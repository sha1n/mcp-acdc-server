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
	mw, err := NewMiddleware(config.AuthSettings{Type: "none"})
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
		Type: "basic",
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

	// Test Unknown
	_, err = NewMiddleware(config.AuthSettings{Type: "unknown"})
	if err == nil {
		t.Error("Expected error for unknown auth type")
	}
}
