# Code Review: Authentication Feature Branch

**Branch:** `auth-and-tls-support-7174407669502239673`  
**Reviewer:** AI Code Review  
**Date:** 2026-01-20

---

## Overview

This branch adds two authentication methods (API key and Basic auth) to the MCP ACDC server. The changes span 8 files with ~547 lines added.

| File                                                                                                         | Purpose                 |
| ------------------------------------------------------------------------------------------------------------ | ----------------------- |
| [middleware.go](file:///Users/shai/code/mcp-acdc-server-go/internal/auth/middleware.go)                      | Auth middleware factory |
| [middleware_test.go](file:///Users/shai/code/mcp-acdc-server-go/internal/auth/middleware_test.go)            | Unit tests              |
| [settings.go](file:///Users/shai/code/mcp-acdc-server-go/internal/config/settings.go)                        | Configuration structs   |
| [settings_test.go](file:///Users/shai/code/mcp-acdc-server-go/internal/config/settings_test.go)              | Config tests            |
| [auth_integration_test.go](file:///Users/shai/code/mcp-acdc-server-go/cmd/mcp-acdc/auth_integration_test.go) | Integration tests       |
| [setup.go](file:///Users/shai/code/mcp-acdc-server-go/cmd/mcp-acdc/setup.go)                                 | Server wiring           |
| [main.go](file:///Users/shai/code/mcp-acdc-server-go/cmd/mcp-acdc/main.go)                                   | Entry point             |
| [run_test.go](file:///Users/shai/code/mcp-acdc-server-go/cmd/mcp-acdc/run_test.go)                           | Run tests               |

---

## Critical Issues

### 1. API Keys in Query String Are a Security Risk

> [!CAUTION]
> **Security Vulnerability**

```go
// middleware.go:45-47
key := r.Header.Get("X-API-Key")
if key == "" {
    key = r.URL.Query().Get("api_key")
}
```

API keys in URLs are logged in server access logs, browser history, proxy logs, and referrer headers. This is a known anti-pattern. **Recommendation:** Remove query parameter support entirely or add explicit documentation warning users about the risks.

---

### 2. Basic Auth Credentials Stored in Plain Text

> [!WARNING]
> **Security Concern**

The `BasicAuthSettings` struct stores username and password as plain strings:

```go
type BasicAuthSettings struct {
    Username string `mapstructure:"username"`
    Password string `mapstructure:"password"`
}
```

While environment variables are common for secrets, this design encourages storing credentials in `.env` files which may accidentally be committed. Consider documenting best practices for secrets management (e.g., using a secrets manager or ensuring `.env` is in `.gitignore`).

---

### 3. Empty Credentials Allowed for Basic Auth

> [!WARNING]
> **Logic Bug**

If `auth.type` is set to `"basic"` but `username` and `password` are not provided, the middleware will accept any request that provides empty credentials:

```go
// An empty password configured will match an empty password provided
subtle.ConstantTimeCompare([]byte(""), []byte("")) == 1
```

**Recommendation:** Validate that `Basic.Username` and `Basic.Password` are non-empty when `Type == "basic"` in `NewMiddleware`:

```go
case "basic":
    if settings.Basic.Username == "" || settings.Basic.Password == "" {
        return nil, fmt.Errorf("basic auth requires non-empty username and password")
    }
    return basicAuthMiddleware(settings.Basic), nil
```

---

### 4. Empty API Keys List Allows All Requests

> [!WARNING]
> **Logic Bug**

If `auth.type` is `"apikey"` but `api_keys` is empty or not configured, the middleware loop never matches, but there's no upfront validation:

```go
// A key will fail the loop, return 401
// But if apiKeys is empty, this is confusing behavior
for _, validKey := range apiKeys {
    // never executes
}
```

This is technically secure (returns 401), but confusing. **Recommendation:** Add validation:

```go
case "apikey":
    if len(settings.APIKeys) == 0 {
        return nil, fmt.Errorf("apikey auth requires at least one API key")
    }
    return apiKeyMiddleware(settings.APIKeys), nil
```

---

## Design Issues

### 5. Missing Documentation for Auth Configuration

> [!IMPORTANT]
> **Documentation Gap**

The [README.md](file:///Users/shai/code/mcp-acdc-server-go/README.md) configuration table does not include the new authentication environment variables:

- `ACDC_MCP_AUTH_TYPE`
- `ACDC_MCP_AUTH_BASIC_USERNAME`
- `ACDC_MCP_AUTH_BASIC_PASSWORD`
- `ACDC_MCP_AUTH_API_KEYS`

**Recommendation:** Update the README with authentication configuration documentation.

---

### 6. No Health Check Endpoint Excluded from Auth

> [!IMPORTANT]
> **Usability Issue**

The auth middleware wraps the entire `sseServer` handler:

```go
// setup.go:29
handler := authMiddleware(sseServer)
```

This means health checks, metrics, or any future `/status` endpoint will also require authentication, which breaks standard Kubernetes liveness/readiness probe patterns.

**Recommendation:** Implement path-based exclusions:
```go
func (m *authMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    if r.URL.Path == "/health" || r.URL.Path == "/ready" {
        m.next.ServeHTTP(w, r)
        return
    }
    m.requireAuth(w, r)
}
```

---

### 7. Branch Name References TLS but No TLS Support Present

The branch is named `auth-and-tls-support-*` but there is no TLS implementation in this branch. The server only listens on plain HTTP:

```go
// setup.go:34
return http.ListenAndServe(addr, handler)
```

**Recommendation:** Either implement TLS or rename the branch to accurately reflect its scope.

---

## Code Style Issues

### 8. Inconsistent Error Handling in BindEnv Calls

```go
// settings.go:63-69
_ = v.BindEnv("search.max_results", "ACDC_MCP_SEARCH_MAX_RESULTS")
_ = v.BindEnv("auth.type", "ACDC_MCP_AUTH_TYPE")
```

The discarded errors are intentional (Viper returns errors only for empty keys, which are hardcoded), but this pattern is inconsistent. Consider adding a brief comment explaining why errors are discarded, or use a helper:

```go
func must(err error) {
    if err != nil {
        panic(err) // programming error
    }
}
```

---

### 9. Import Order in setup.go

```go
import (
    "fmt"
    "log/slog"
    "os"

    "github.com/mark3labs/mcp-go/server"
    // ... other imports
    "net/http"  // ← Standard library import mixed with third-party
)
```

**Recommendation:** Move `net/http` to the standard library group per Go conventions.

---

### 10. Magic String Constants

Auth types are hardcoded strings scattered across files:

```go
case "none", "":
case "basic":
case "apikey":
settings.Auth.Type != "none"
```

**Recommendation:** Define constants:
```go
const (
    AuthTypeNone   = "none"
    AuthTypeBasic  = "basic"
    AuthTypeAPIKey = "apikey"
)
```

---

## Testing Issues

### 11. Integration Tests Use Fixed Sleep

```go
// auth_integration_test.go:56
time.Sleep(100 * time.Millisecond) // Wait for start
```

This is fragile. On slow CI systems, 100ms may not be enough.

**Recommendation:** Use a proper readiness check:
```go
func waitForServer(url string, timeout time.Duration) error {
    deadline := time.Now().Add(timeout)
    for time.Now().Before(deadline) {
        resp, err := http.Get(url + "/health")
        if err == nil && resp.StatusCode == 200 {
            return nil
        }
        time.Sleep(10 * time.Millisecond)
    }
    return errors.New("server did not become ready")
}
```

---

### 12. Missing Test: NewMiddleware with APIKey Type

The `TestNewMiddleware` function tests:
- `none` type ✓
- `basic` type ✓
- `unknown` type ✓

But does **not** test the `apikey` type through `NewMiddleware`. While `TestAPIKeyAuth` tests the underlying middleware, there's no coverage for the factory function with `Type: "apikey"`.

---

### 13. Settings Tests Modify Global Environment

```go
if err := os.Setenv("ACDC_MCP_PORT", "9090"); err != nil {
    t.Fatal(err)
}
```

Tests that modify environment variables can interfere with each other in parallel test runs. Consider using `t.Setenv()` (Go 1.17+) which automatically cleans up:

```go
t.Setenv("ACDC_MCP_PORT", "9090")
```

---

### 14. Integration Tests Don't Shut Down Server

```go
go func() {
    if err := StartSSEServer(mcpServer, settings); err != nil && err != http.ErrServerClosed {
        t.Logf("Server error: %v", err)
    }
}()
```

The server is never shut down after tests complete. This leaks goroutines and ports.

**Recommendation:** Return a shutdown function or use `httptest.Server`:
```go
srv := &http.Server{Addr: addr, Handler: handler}
go srv.ListenAndServe()
defer srv.Close()
```

---

## Minor Nits

### 15. Line Length in Middleware

Line 31 in `middleware.go` is 156 characters:

```go
if !ok || subtle.ConstantTimeCompare([]byte(user), []byte(settings.Username)) != 1 || subtle.ConstantTimeCompare([]byte(pass), []byte(settings.Password)) != 1 {
```

**Recommendation:** Break into multiple lines:
```go
userMatch := subtle.ConstantTimeCompare([]byte(user), []byte(settings.Username)) == 1
passMatch := subtle.ConstantTimeCompare([]byte(pass), []byte(settings.Password)) == 1
if !ok || !userMatch || !passMatch {
```

---

### 16. Test Assertions Could Use Table-Driven Pattern

Several tests repeat similar patterns:
```go
if w.Code != http.StatusOK {
    t.Errorf("Expected status 200, got %d", w.Code)
}
```

Consider using subtests and table-driven testing for cleaner, more maintainable tests.

---

## Positive Observations ✅

1. **Constant-time comparison** used correctly in both auth methods to prevent timing attacks
2. **Good separation of concerns** between middleware, config, and setup
3. **Dependency injection** pattern in `RunParams` enables testability
4. **API key trimming** handles whitespace gracefully in comma-separated lists
5. **Integration tests** provide good end-to-end coverage of real HTTP flows
6. **Clean middleware pattern** follows standard Go `func(http.Handler) http.Handler` signature

---

## Summary

| Category     | Count             |
| ------------ | ----------------- |
| 🔴 Critical   | 1 (security)      |
| 🟠 Warnings   | 3 (bugs/security) |
| 🟡 Design     | 3                 |
| 🔵 Code Style | 3                 |
| ⚪ Testing    | 4                 |
| ⚫ Minor      | 2                 |

**Overall Assessment:** The implementation is functional and follows reasonable Go patterns, but has security concerns that should be addressed before merging. The empty credentials edge case and API keys in query strings are the most pressing issues. Documentation and test robustness should also be improved.
