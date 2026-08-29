package testkit

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/app"
	"github.com/sha1n/mcp-acdc-server/internal/content"
	"github.com/spf13/pflag"
)

type Service interface {
	Start() (map[string]any, error)
	Stop() error
	GetName() string
}

type TestEnvContext interface {
	GetProperties() map[string]any
	GetProperty(name string) (any, bool)
}

type TestEnv interface {
	Start() (map[string]any, error)
	Stop() error
	GetContext() TestEnvContext
}

type testEnvContextImpl struct {
	properties map[string]any
}

func (c *testEnvContextImpl) GetProperties() map[string]any {
	return c.properties
}

func (c *testEnvContextImpl) GetProperty(name string) (any, bool) {
	val, ok := c.properties[name]
	return val, ok
}

type testEnvImpl struct {
	services []Service
	context  *testEnvContextImpl
}

func NewTestEnv(services ...Service) TestEnv {
	return &testEnvImpl{
		services: services,
		context:  &testEnvContextImpl{properties: make(map[string]any)},
	}
}

func (e *testEnvImpl) Start() (map[string]any, error) {
	for _, s := range e.services {
		props, err := s.Start()
		if err != nil {
			return nil, err
		}
		for k, v := range props {
			e.context.properties[k] = v
		}
	}
	return e.context.properties, nil
}

func (e *testEnvImpl) Stop() error {
	var lastErr error
	// Stop in reverse order
	for i := len(e.services) - 1; i >= 0; i-- {
		if err := e.services[i].Stop(); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

func (e *testEnvImpl) GetContext() TestEnvContext {
	return e.context
}

// GetFreePort returns a free port from the kernel
func GetFreePort() (int, error) {
	return getFreePortWithAddr("localhost:0")
}

// MustGetFreePort returns a free port or fails the test
func MustGetFreePort(t testing.TB) int {
	t.Helper()
	port, err := GetFreePort()
	if err != nil {
		t.Fatalf("Failed to get free port: %v", err)
	}
	return port
}

func getFreePortWithAddr(addrStr string) (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", addrStr)
	if err != nil {
		return 0, err
	}

	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// ContentDirOptions configures CreateTestContentDir
type ContentDirOptions struct {
	Metadata   string            // Custom metadata YAML (uses default if empty)
	Resources  map[string]string // filename -> content (no resources if nil)
	Prompts    map[string]string // filename -> content (no prompts if nil)
	Files      map[string]string // path (relative to content root) -> content (no extra files if nil)
	ResultMode string            // Search result mode to request via NewStdioTestClient (process default if empty)
	CrossRef   bool              // Cross-reference link rewriting to request via NewStdioTestClient (process default if false)
}

// DefaultMetadata returns the default test metadata YAML
func DefaultMetadata() string {
	return `server:
  name: test
  version: "1.0"
  instructions: test instructions
tools: []
`
}

// CreateTestContentDir creates a temp content directory with metadata and optional resources.
// Returns the content directory path.
func CreateTestContentDir(t testing.TB, opts *ContentDirOptions) string {
	t.Helper()

	tempDir := t.TempDir()
	contentDir := filepath.Join(tempDir, "content")
	cp := content.NewContentProvider(contentDir)

	if err := os.MkdirAll(cp.ResourcesDir, 0755); err != nil {
		t.Fatalf("Failed to create resources dir: %v", err)
	}

	metadata := DefaultMetadata()
	if opts != nil && opts.Metadata != "" {
		metadata = opts.Metadata
	}
	if err := os.WriteFile(cp.ConfigFile, []byte(metadata), 0644); err != nil {
		t.Fatalf("Failed to write metadata: %v", err)
	}

	if opts == nil {
		return contentDir
	}

	writeTestFiles(t, cp.ResourcesDir, opts.Resources, "resource")

	if opts.Prompts != nil {
		if err := os.MkdirAll(cp.PromptsDir, 0755); err != nil {
			t.Fatalf("Failed to create prompts dir: %v", err)
		}
		writeTestFiles(t, cp.PromptsDir, opts.Prompts, "prompt")
	}

	writeExtraContentFiles(t, contentDir, opts.Files)

	return contentDir
}

// writeExtraContentFiles writes ContentDirOptions.Files, which are addressed
// relative to the content root and must stay confined to it.
func writeExtraContentFiles(t testing.TB, contentDir string, files map[string]string) {
	t.Helper()

	for name, body := range files {
		if !filepath.IsLocal(name) {
			t.Fatalf("Invalid Files path %q: must be relative and confined to the content root", name)
			continue
		}
		writeTestFile(t, filepath.Join(contentDir, filepath.Clean(name)), name, body, "file")
	}
}

func writeTestFiles(t testing.TB, dir string, files map[string]string, label string) {
	t.Helper()

	for name, body := range files {
		writeTestFile(t, filepath.Join(dir, name), name, body, label)
	}
}

// writeTestFile writes body to path, creating parent directories. name and
// label identify the file in failure messages.
func writeTestFile(t testing.TB, path, name, body, label string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("Failed to create parent dir for %s %s: %v", label, name, err)
	}
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatalf("Failed to write %s %s: %v", label, name, err)
	}
}

// FlagOptions configures NewTestFlags
type FlagOptions struct {
	Port       int    // Uses free port if 0
	Transport  string // Defaults to "sse"
	AuthType   string // Defaults to "none"
	Host       string // Defaults to "localhost"
	Scheme     string // Defaults to "" (uses config default "acdc")
	ResultMode string // Defaults to "" (uses config default "references"); sets --search-result-mode only when non-empty
	CrossRef   bool   // Defaults to false; sets --cross-ref only when true
}

// NewTestFlags creates a configured pflag.FlagSet for testing
func NewTestFlags(t testing.TB, contentDir string, opts *FlagOptions) *pflag.FlagSet {
	t.Helper()

	resolved := resolveFlagOptions(opts)
	if resolved.Port == 0 {
		resolved.Port = MustGetFreePort(t)
	}

	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	app.RegisterFlags(flags)

	_ = flags.Set("port", fmt.Sprintf("%d", resolved.Port))
	_ = flags.Set("content-dir", contentDir)
	_ = flags.Set("transport", resolved.Transport)
	_ = flags.Set("auth-type", resolved.AuthType)
	_ = flags.Set("host", resolved.Host)
	if resolved.Scheme != "" {
		_ = flags.Set("uri-scheme", resolved.Scheme)
	}
	if resolved.ResultMode != "" {
		_ = flags.Set("search-result-mode", resolved.ResultMode)
	}
	if resolved.CrossRef {
		_ = flags.Set("cross-ref", "true")
	}

	return flags
}

// resolveFlagOptions applies the defaults documented on FlagOptions. A zero Port
// is left for the caller to fill with a free port.
func resolveFlagOptions(opts *FlagOptions) FlagOptions {
	resolved := FlagOptions{
		Transport: "sse",
		AuthType:  "none",
		Host:      "localhost",
	}
	if opts == nil {
		return resolved
	}

	resolved.Port = opts.Port
	resolved.Scheme = opts.Scheme
	resolved.ResultMode = opts.ResultMode
	resolved.CrossRef = opts.CrossRef
	if opts.Transport != "" {
		resolved.Transport = opts.Transport
	}
	if opts.AuthType != "" {
		resolved.AuthType = opts.AuthType
	}
	if opts.Host != "" {
		resolved.Host = opts.Host
	}

	return resolved
}
