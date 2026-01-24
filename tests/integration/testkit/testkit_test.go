package testkit

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/app"
	"github.com/spf13/pflag"
)

func TestGetFreePort_Errors(t *testing.T) {
	_, err := getFreePortWithAddr("invalid-address:0")
	if err == nil {
		t.Error("Expected error for invalid address")
	}

	// Port 1 usually requires root and might fail to listen
	_, err = getFreePortWithAddr("localhost:1")
	if err == nil {
		// If it succeeded, it's weird but possible, but usually fails
		t.Log("Listen on port 1 succeeded (maybe running as root?)")
	}
}

func TestGetFreePort(t *testing.T) {
	port, err := GetFreePort()
	if err != nil {
		t.Fatalf("GetFreePort failed: %v", err)
	}
	if port <= 0 {
		t.Errorf("Expected positive port, got %d", port)
	}

	// Verify port is actually free by listening on it
	l, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", port))
	if err != nil {
		t.Fatalf("Could not listen on port returned by GetFreePort: %v", err)
	}
	_ = l.Close()
}

type mockService struct {
	name      string
	startFunc func() (map[string]any, error)
	stopFunc  func() error
}

func (s *mockService) Start() (map[string]any, error) { return s.startFunc() }
func (s *mockService) Stop() error                    { return s.stopFunc() }
func (s *mockService) GetName() string                { return s.name }

func TestTestEnv_Lifecycle(t *testing.T) {
	s1 := &mockService{
		name: "s1",
		startFunc: func() (map[string]any, error) {
			return map[string]any{"p1": "v1"}, nil
		},
		stopFunc: func() error { return nil },
	}
	s2 := &mockService{
		name: "s2",
		startFunc: func() (map[string]any, error) {
			return map[string]any{"p2": "v2"}, nil
		},
		stopFunc: func() error { return nil },
	}

	env := NewTestEnv(s1, s2)
	props, err := env.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	if props["p1"] != "v1" || props["p2"] != "v2" {
		t.Errorf("Unexpected properties: %v", props)
	}

	ctx := env.GetContext()
	val, ok := ctx.GetProperty("p1")
	if !ok || val != "v1" {
		t.Errorf("GetProperty failed")
	}

	allProps := ctx.GetProperties()
	if len(allProps) != 2 {
		t.Errorf("GetProperties failed")
	}

	if err := env.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
}

func TestTestEnv_StartError(t *testing.T) {
	s1 := &mockService{
		name: "s1",
		startFunc: func() (map[string]any, error) {
			return nil, errors.New("start failed")
		},
		stopFunc: func() error { return nil },
	}

	env := NewTestEnv(s1)
	_, err := env.Start()
	if err == nil || err.Error() != "start failed" {
		t.Errorf("Expected start failed error, got %v", err)
	}
}

func TestTestEnv_StopError(t *testing.T) {
	s1 := &mockService{
		name: "s1",
		startFunc: func() (map[string]any, error) {
			return nil, nil
		},
		stopFunc: func() error { return errors.New("stop failed") },
	}

	env := NewTestEnv(s1)
	_, _ = env.Start()
	err := env.Stop()
	if err == nil || err.Error() != "stop failed" {
		t.Errorf("Expected stop failed error, got %v", err)
	}
}

func TestACDCService_Discovery(t *testing.T) {
	tempDir := t.TempDir()
	contentDir := filepath.Join(tempDir, "content")
	resourcesDir := filepath.Join(contentDir, "mcp-resources")
	_ = os.MkdirAll(resourcesDir, 0755)

	metadataContent := `server: { name: test, version: 1.0, instructions: inst }`
	_ = os.WriteFile(filepath.Join(contentDir, "mcp-metadata.yaml"), []byte(metadataContent), 0644)

	port, _ := GetFreePort()
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	app.RegisterFlags(flags)
	_ = flags.Set("port", fmt.Sprintf("%d", port))
	_ = flags.Set("content-dir", contentDir)
	_ = flags.Set("transport", "sse")
	_ = flags.Set("auth-type", "none")

	service := NewACDCService("test-acdc", flags)
	if service.GetName() != "test-acdc" {
		t.Errorf("Expected name test-acdc, got %s", service.GetName())
	}

	env := NewTestEnv(service)
	_, err := env.Start()
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() { _ = env.Stop() }()

	baseURL, ok := env.GetContext().GetProperty("acdc.baseURL")
	if !ok {
		t.Fatal("BaseURL not found")
	}

	resp, err := http.Get(baseURL.(string) + "/sse")
	if err != nil {
		t.Fatalf("Failed to call server: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("Expected 200, got %d", resp.StatusCode)
	}
}

func TestACDCService_StartExit(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	app.RegisterFlags(flags)

	service := NewACDCService("exit", flags)
	acdc := service.(*acdcService)
	acdc.runner = func(ctx context.Context, params app.RunParams, flags *pflag.FlagSet, version string) error {
		return errors.New("exit early")
	}

	_, err := service.Start()
	if err == nil || err.Error() != "server exited unexpectedly: exit early" {
		t.Errorf("Expected exit early error, got %v", err)
	}
}

func TestACDCService_StartTimeout(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	app.RegisterFlags(flags)

	service := NewACDCService("timeout", flags)
	acdc := service.(*acdcService)
	acdc.StartTimeout = 100 * time.Millisecond
	acdc.runner = func(ctx context.Context, params app.RunParams, flags *pflag.FlagSet, version string) error {
		time.Sleep(1 * time.Second)
		return nil
	}

	_, err := service.Start()
	if err == nil {
		t.Error("Expected timeout error")
	}
}

func TestACDCService_StopTimeout(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	app.RegisterFlags(flags)

	service := NewACDCService("stop-timeout", flags)
	acdc := service.(*acdcService)
	acdc.StopDelay = 100 * time.Millisecond
	acdc.runner = func(ctx context.Context, params app.RunParams, flags *pflag.FlagSet, version string) error {
		time.Sleep(1 * time.Second)
		return nil
	}

	// Mock Start to avoid polling
	go func() {
		acdc.errChan <- acdc.runner(context.Background(), app.RunParams{}, flags, "test")
	}()

	err := service.Stop()
	if err == nil {
		t.Error("Expected stop timeout error")
	}
}

func TestACDCService_StopError(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	app.RegisterFlags(flags)

	service := NewACDCService("stop-error", flags)
	acdc := service.(*acdcService)
	acdc.runner = func(ctx context.Context, params app.RunParams, flags *pflag.FlagSet, version string) error {
		return errors.New("stop error")
	}

	// Mock Start
	go func() {
		acdc.errChan <- acdc.runner(context.Background(), app.RunParams{}, flags, "test")
	}()

	err := service.Stop()
	if err == nil || err.Error() != "stop error" {
		t.Errorf("Expected stop error, got %v", err)
	}
}

func TestMustGetFreePort(t *testing.T) {
	port := MustGetFreePort(t)
	if port <= 0 {
		t.Errorf("Expected positive port, got %d", port)
	}
}

func TestCreateTestContentDir_Defaults(t *testing.T) {
	contentDir := CreateTestContentDir(t, nil)

	// Verify directory structure
	if _, err := os.Stat(contentDir); os.IsNotExist(err) {
		t.Error("Content dir not created")
	}
	if _, err := os.Stat(filepath.Join(contentDir, "mcp-resources")); os.IsNotExist(err) {
		t.Error("Resources dir not created")
	}
	if _, err := os.Stat(filepath.Join(contentDir, "mcp-metadata.yaml")); os.IsNotExist(err) {
		t.Error("Metadata file not created")
	}
}

func TestCreateTestContentDir_WithOptions(t *testing.T) {
	opts := &ContentDirOptions{
		Metadata: `server: { name: custom, version: 2.0, instructions: custom }`,
		Resources: map[string]string{
			"test.md": "---\nname: Test\ndescription: Test resource\n---\nContent",
		},
		Prompts: map[string]string{
			"test-prompt.md": "---\nname: TestPrompt\ndescription: Test prompt\n---\nContent",
		},
	}
	contentDir := CreateTestContentDir(t, opts)

	// Verify custom metadata
	data, err := os.ReadFile(filepath.Join(contentDir, "mcp-metadata.yaml"))
	if err != nil {
		t.Fatalf("Failed to read metadata: %v", err)
	}
	if string(data) != opts.Metadata {
		t.Errorf("Unexpected metadata: %s", data)
	}

	// Verify resource file
	if _, err := os.Stat(filepath.Join(contentDir, "mcp-resources", "test.md")); os.IsNotExist(err) {
		t.Error("Resource file not created")
	}

	// Verify prompt file
	if _, err := os.Stat(filepath.Join(contentDir, "mcp-prompts", "test-prompt.md")); os.IsNotExist(err) {
		t.Error("Prompt file not created")
	}
}

func TestNewTestFlags_Defaults(t *testing.T) {
	contentDir := CreateTestContentDir(t, nil)
	flags := NewTestFlags(t, contentDir, nil)

	transport, _ := flags.GetString("transport")
	authType, _ := flags.GetString("auth-type")
	host, _ := flags.GetString("host")
	port, _ := flags.GetInt("port")
	flagContentDir, _ := flags.GetString("content-dir")

	if transport != "sse" {
		t.Errorf("Expected transport 'sse', got '%s'", transport)
	}
	if authType != "none" {
		t.Errorf("Expected auth-type 'none', got '%s'", authType)
	}
	if host != "localhost" {
		t.Errorf("Expected host 'localhost', got '%s'", host)
	}
	if port <= 0 {
		t.Errorf("Expected positive port, got %d", port)
	}
	if flagContentDir != contentDir {
		t.Errorf("Expected content-dir '%s', got '%s'", contentDir, flagContentDir)
	}
}

func TestNewTestFlags_WithOptions(t *testing.T) {
	contentDir := CreateTestContentDir(t, nil)
	opts := &FlagOptions{
		Port:      9999,
		Transport: "stdio",
		AuthType:  "apikey",
		Host:      "127.0.0.1",
	}
	flags := NewTestFlags(t, contentDir, opts)

	transport, _ := flags.GetString("transport")
	authType, _ := flags.GetString("auth-type")
	host, _ := flags.GetString("host")
	port, _ := flags.GetInt("port")

	if transport != "stdio" {
		t.Errorf("Expected transport 'stdio', got '%s'", transport)
	}
	if authType != "apikey" {
		t.Errorf("Expected auth-type 'apikey', got '%s'", authType)
	}
	if host != "127.0.0.1" {
		t.Errorf("Expected host '127.0.0.1', got '%s'", host)
	}
	if port != 9999 {
		t.Errorf("Expected port 9999, got %d", port)
	}
}

type mockTB struct {
	*testing.T
	failed  bool
	tempDir string
}

func (m *mockTB) Fatalf(format string, args ...interface{}) {
	m.failed = true
}

func (m *mockTB) TempDir() string {
	return m.tempDir
}

func TestCreateTestContentDir_PromptsMkdirError(t *testing.T) {
	tempDir := t.TempDir()
	contentDir := filepath.Join(tempDir, "mkdir-err")
	_ = os.MkdirAll(filepath.Join(contentDir, "content"), 0755)

	// Create a file where a directory is expected to cause MkdirAll failure
	promptsDir := filepath.Join(contentDir, "content", "mcp-prompts")
	_ = os.WriteFile(promptsDir, []byte("not a directory"), 0644)

	mt := &mockTB{T: t, tempDir: contentDir}
	opts := &ContentDirOptions{
		Prompts: map[string]string{"test.md": "content"},
	}

	CreateTestContentDir(mt, opts)

	if !mt.failed {
		t.Error("Expected failure for prompts MkdirAll")
	}
}

func TestCreateTestContentDir_PromptsWriteError(t *testing.T) {
	tempDir := t.TempDir()
	contentDir := filepath.Join(tempDir, "write-err")
	_ = os.MkdirAll(filepath.Join(contentDir, "content"), 0755)

	mt := &mockTB{T: t, tempDir: contentDir}

	opts := &ContentDirOptions{
		Prompts: map[string]string{"test.md": "content"},
	}

	// Create a directory where a file is expected to cause WriteFile failure
	promptsDir := filepath.Join(contentDir, "content", "mcp-prompts")
	_ = os.MkdirAll(filepath.Join(promptsDir, "test.md"), 0755)

	CreateTestContentDir(mt, opts)

	if !mt.failed {
		t.Error("Expected failure for prompts WriteFile")
	}
}
