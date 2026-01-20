package resources

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sha1n/mcp-acdc-server-go/internal/content"
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

	// Test GetAllResourceContents
	t.Run("GetAllResourceContents", func(t *testing.T) {
		got := p.GetAllResourceContents()
		if len(got) != 1 {
			t.Errorf("GetAllResourceContents returned %d items, want 1", len(got))
		}
		if got[0]["content"] != "Body" {
			t.Errorf("GetAllResourceContents content = %q, want %q", got[0]["content"], "Body")
		}
	})
}

func TestResourceProvider_GetAllResourceContents_ErrorHandling(t *testing.T) {
	defs := []ResourceDefinition{
		{
			URI:      "acdc://valid",
			Name:     "Valid",
			FilePath: "non-existent-but-wont-be-called-if-we-mock-it", // wait, ReadResource calls LoadMarkdownWithFrontmatter which reads from disk
		},
		{
			URI:      "acdc://invalid",
			Name:     "Invalid",
			FilePath: "non-existent.md",
		},
	}

	// We can't easily mock content.NewContentProvider("").LoadMarkdownWithFrontmatter(defn.FilePath)
	// Because it's called inside ReadResource.
	// But we can create a real file for the first one and let the second one fail.

	tmp := t.TempDir()
	validFile := filepath.Join(tmp, "valid.md")
	_ = os.WriteFile(validFile, []byte("---\nname: Valid\ndescription: D\n---\nValidBody"), 0644)

	defs[0].FilePath = validFile

	p := NewResourceProvider(defs)
	got := p.GetAllResourceContents()

	if len(got) != 1 {
		t.Errorf("GetAllResourceContents returned %d items, want 1 (successful one)", len(got))
		return
	}

	if got[0]["uri"] != "acdc://valid" {
		t.Errorf("Expected uri 'acdc://valid', got '%s'", got[0]["uri"])
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

	defs, err := DiscoverResources(cp)
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
