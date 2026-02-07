package resources

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sha1n/mcp-acdc-server/internal/content"
	"github.com/sha1n/mcp-acdc-server/internal/domain"
)

func TestResourceProvider_Methods(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "test.md")
	if err := os.WriteFile(f, []byte("---\nname: N\ndescription: D\n---\nBody"), 0644); err != nil {
		t.Fatal(err)
	}

	defs := []ResourceDefinition{
		{
			URI:         "acdc://test",
			Name:        "Test Resource",
			Description: "Desc",
			MIMEType:    "text/markdown",
			FilePath:    f,
		},
	}

	p := NewResourceProvider(defs)

	// Test ListResources
	t.Run("ListResources", func(t *testing.T) {
		list := p.ListResources()
		if len(list) != 1 {
			t.Errorf("ListResources returned %d items, want 1", len(list))
		}
		if list[0].URI != defs[0].URI {
			t.Errorf("ListResources URI = %s, want %s", list[0].URI, defs[0].URI)
		}
	})

	// Test ReadResource
	t.Run("ReadResource", func(t *testing.T) {
		got, err := p.ReadResource(defs[0].URI)
		if err != nil {
			t.Errorf("ReadResource error = %v", err)
		}
		if got != "Body" {
			t.Errorf("ReadResource content = %q, want %q", got, "Body")
		}
	})

	// Test ReadResource Unknown
	t.Run("ReadResource Unknown", func(t *testing.T) {
		_, err := p.ReadResource("unknown")
		if err == nil {
			t.Error("ReadResource expected error for unknown URI")
		}
	})

	// Test StreamResources
	t.Run("StreamResources", func(t *testing.T) {
		ch := make(chan domain.Document, 10)
		go func() {
			defer close(ch)
			_ = p.StreamResources(context.Background(), ch)
		}()

		var docs []domain.Document
		for d := range ch {
			docs = append(docs, d)
		}

		if len(docs) != 1 {
			t.Errorf("StreamResources returned %d items, want 1", len(docs))
		}
		if docs[0].Content != "Body" {
			t.Errorf("StreamResources content = %q, want %q", docs[0].Content, "Body")
		}
	})
}

func TestResourceProvider_StreamResources_ErrorHandling(t *testing.T) {
	defs := []ResourceDefinition{
		{
			URI:      "acdc://valid",
			Name:     "Valid",
			FilePath: "valid.md",
		},
		{
			URI:      "acdc://invalid",
			Name:     "Invalid",
			FilePath: "invalid.md",
		},
	}

	tempDir := t.TempDir()
	validFile := filepath.Join(tempDir, "valid.md")
	// content requires frontmatter to be parsed correctly by content provider if it uses LoadMarkdownWithFrontmatter?
	// But ReadResource uses content.NewContentProvider("").LoadMarkdownWithFrontmatter(defn.FilePath)
	// which expects frontmatter.
	_ = os.WriteFile(validFile, []byte("---\nname: Valid\n---\nBody"), 0644)
	defs[0].FilePath = validFile

	p := NewResourceProvider(defs)

	ch := make(chan domain.Document, 10)
	go func() {
		defer close(ch)
		_ = p.StreamResources(context.Background(), ch)
	}()

	var got []domain.Document
	for d := range ch {
		got = append(got, d)
	}

	if len(got) != 1 {
		t.Errorf("StreamResources returned %d items, want 1 (successful one)", len(got))
		return
	}

	if got[0].URI != "acdc://valid" {
		t.Errorf("Expected uri 'acdc://valid', got '%s'", got[0].URI)
	}
}

func TestResourceProvider_StreamResources_ContextCancellation(t *testing.T) {
	defs := []ResourceDefinition{
		{
			URI:  "acdc://1",
			Name: "1",
		},
	}
	p := NewResourceProvider(defs)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	ch := make(chan domain.Document)
	err := p.StreamResources(ctx, ch)
	if err == nil {
		t.Error("Expected error on context cancellation, got nil")
	}
	if err != context.Canceled {
		t.Errorf("Expected context.Canceled, got %v", err)
	}
}

func TestResourceProvider_StreamResources_ContextCancellation_Blocked(t *testing.T) {
	defs := []ResourceDefinition{
		{
			URI:  "acdc://1",
			Name: "1",
			// FilePath needs to exist for ReadResource to succeed and reach the send block
			FilePath: "valid.md",
		},
	}
	tempDir := t.TempDir()
	defs[0].FilePath = filepath.Join(tempDir, "valid.md")
	_ = os.WriteFile(defs[0].FilePath, []byte("---\nname: 1\n---\nBody"), 0644)

	p := NewResourceProvider(defs)

	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan domain.Document) // Unbuffered, so send will block

	errChan := make(chan error)
	go func() {
		errChan <- p.StreamResources(ctx, ch)
	}()

	// Wait a bit to ensure ReadResource completes and we are blocked on sending
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-errChan:
		if err != context.Canceled {
			t.Errorf("Expected context.Canceled, got %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for StreamResources to return")
	}
}

func TestDiscoverResources(t *testing.T) {
	// Setup directory structure
	tmp := t.TempDir()
	resDir := filepath.Join(tmp, "mcp-resources")
	if err := os.MkdirAll(resDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Valid resource
	validRes := filepath.Join(resDir, "valid.md")
	if err := os.WriteFile(validRes, []byte("---\nname: Valid\ndescription: D\n---\nContent"), 0644); err != nil {
		t.Fatal(err)
	}

	// Invalid resource (no name)
	invalidRes := filepath.Join(resDir, "invalid.md")
	if err := os.WriteFile(invalidRes, []byte("---\ndescription: D\n---\nContent"), 0644); err != nil {
		t.Fatal(err)
	}

	// Not markdown
	txtRes := filepath.Join(resDir, "files.txt")
	if err := os.WriteFile(txtRes, []byte("text"), 0644); err != nil {
		t.Fatal(err)
	}

	// Subdir (should be skipped by implementation if WalkDir doesn't recurse? WalkDir recurses. Implementation checks .md extension)
	// But let's check if it handles subdirs correctly.
	subDir := filepath.Join(resDir, "sub")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	subRes := filepath.Join(subDir, "sub.md")
	if err := os.WriteFile(subRes, []byte("---\nname: Sub\ndescription: D\n---\nSubContent"), 0644); err != nil {
		t.Fatal(err)
	}

	cp := content.NewContentProvider(tmp)

	defs, err := DiscoverResources(cp, "acdc")
	if err != nil {
		t.Fatalf("DiscoverResources error = %v", err)
	}

	// Expect 2 resources: valid.md and sub.md
	if len(defs) != 2 {
		t.Errorf("DiscoverResources found %d items, want 2", len(defs))
	}

	// Check URIs (should use forward slashes)
	// "valid" and "sub/sub"
	uris := make(map[string]bool)
	for _, d := range defs {
		uris[d.URI] = true
	}

	if !uris["acdc://valid"] {
		t.Error("Missing acdc://valid")
	}
	if !uris["acdc://sub/sub"] {
		t.Error("Missing acdc://sub/sub")
	}
}

func TestDiscoverResources_CustomScheme(t *testing.T) {
	tmp := t.TempDir()
	resDir := filepath.Join(tmp, "mcp-resources")
	if err := os.MkdirAll(resDir, 0755); err != nil {
		t.Fatal(err)
	}

	validRes := filepath.Join(resDir, "doc.md")
	if err := os.WriteFile(validRes, []byte("---\nname: Doc\ndescription: D\n---\nContent"), 0644); err != nil {
		t.Fatal(err)
	}

	cp := content.NewContentProvider(tmp)

	defs, err := DiscoverResources(cp, "my-custom")
	if err != nil {
		t.Fatalf("DiscoverResources error = %v", err)
	}

	if len(defs) != 1 {
		t.Fatalf("DiscoverResources found %d items, want 1", len(defs))
	}

	if defs[0].URI != "my-custom://doc" {
		t.Errorf("Expected URI 'my-custom://doc', got '%s'", defs[0].URI)
	}
}
