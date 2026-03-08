package prompts

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/content"
	"github.com/stretchr/testify/assert"
)

func TestDiscoverPrompts(t *testing.T) {
	t.Run("ValidPrompt", func(t *testing.T) {
		tempDir := t.TempDir()
		promptsDir := filepath.Join(tempDir, "mcp-prompts")
		_ = os.MkdirAll(promptsDir, 0755)
		mdContent := `---
name: test-prompt
description: A test prompt
arguments:
  - name: arg1
    description: First argument
    required: true
---
Hello {{.arg1}}`
		err := os.WriteFile(filepath.Join(promptsDir, "test.md"), []byte(mdContent), 0644)
		assert.NoError(t, err)

		cp := content.NewContentProvider(tempDir)
		defs, err := DiscoverPrompts(cp)
		assert.NoError(t, err)
		assert.Len(t, defs, 1)
		assert.Equal(t, "test-prompt", defs[0].Name)
		assert.NotNil(t, defs[0].Template)
	})

	t.Run("InvalidTemplate", func(t *testing.T) {
		tempDir := t.TempDir()
		promptsDir := filepath.Join(tempDir, "mcp-prompts")
		_ = os.MkdirAll(promptsDir, 0755)
		mdContent := `---
name: bad-template
description: d
---
Hello {{.unclosed`
		err := os.WriteFile(filepath.Join(promptsDir, "bad_tmpl.md"), []byte(mdContent), 0644)
		assert.NoError(t, err)

		cp := content.NewContentProvider(tempDir)
		defs, err := DiscoverPrompts(cp)
		assert.NoError(t, err)
		assert.Empty(t, defs)
	})

	t.Run("ResilientWalking", func(t *testing.T) {
		tempDir := t.TempDir()
		cp := content.NewContentProvider(tempDir)
		_, err := DiscoverPrompts(cp)
		assert.NoError(t, err)
	})

	t.Run("SubDirAndNonMd", func(t *testing.T) {
		tempDir := t.TempDir()
		promptsDir := filepath.Join(tempDir, "mcp-prompts")
		_ = os.MkdirAll(promptsDir, 0755)
		subDir := filepath.Join(promptsDir, "sub")
		_ = os.MkdirAll(subDir, 0755)
		_ = os.WriteFile(filepath.Join(subDir, "sub.md"), []byte("---\nname: sub\ndescription: d\n---\nHello"), 0644)
		_ = os.WriteFile(filepath.Join(promptsDir, "ignore.txt"), []byte("ignore"), 0644)

		cp := content.NewContentProvider(tempDir)
		defs, err := DiscoverPrompts(cp)
		assert.NoError(t, err)
		assert.Len(t, defs, 1)
		assert.Equal(t, "sub", defs[0].Name)
	})

	t.Run("MissingMetadata", func(t *testing.T) {
		tempDir := t.TempDir()
		promptsDir := filepath.Join(tempDir, "mcp-prompts")
		_ = os.MkdirAll(promptsDir, 0755)
		// Missing name
		_ = os.WriteFile(filepath.Join(promptsDir, "no_name.md"), []byte("---\ndescription: d\n---\nHello"), 0644)
		// Missing description
		_ = os.WriteFile(filepath.Join(promptsDir, "no_desc.md"), []byte("---\nname: n\n---\nHello"), 0644)

		cp := content.NewContentProvider(tempDir)
		defs, err := DiscoverPrompts(cp)
		assert.NoError(t, err)
		assert.Empty(t, defs)
	})

	t.Run("InvalidArgumentFormats", func(t *testing.T) {
		tempDir := t.TempDir()
		promptsDir := filepath.Join(tempDir, "mcp-prompts")
		_ = os.MkdirAll(promptsDir, 0755)
		// Args not a slice
		_ = os.WriteFile(filepath.Join(promptsDir, "bad_args1.md"), []byte("---\nname: n1\ndescription: d1\narguments: not-a-slice\n---\nHello"), 0644)
		// Arg not a map
		_ = os.WriteFile(filepath.Join(promptsDir, "bad_args2.md"), []byte("---\nname: n2\ndescription: d2\narguments:\n  - not-a-map\n---\nHello"), 0644)
		// Arg missing name
		_ = os.WriteFile(filepath.Join(promptsDir, "bad_args3.md"), []byte("---\nname: n3\ndescription: d3\narguments:\n  - description: no-name\n---\nHello"), 0644)
		// Arg required explicit false
		_ = os.WriteFile(filepath.Join(promptsDir, "bad_args4.md"), []byte("---\nname: n4\ndescription: d4\narguments:\n  - name: a4\n    required: false\n---\nHello"), 0644)

		cp := content.NewContentProvider(tempDir)
		defs, err := DiscoverPrompts(cp)
		assert.NoError(t, err)
		assert.Len(t, defs, 4)

		for _, d := range defs {
			switch d.Name {
			case "n1", "n2", "n3":
				assert.Empty(t, d.Arguments, "Should have no arguments for %s", d.Name)
			case "n4":
				assert.Len(t, d.Arguments, 1)
				assert.False(t, d.Arguments[0].Required)
			}
		}
	})

	t.Run("InvalidFrontmatter", func(t *testing.T) {
		tempDir := t.TempDir()
		promptsDir := filepath.Join(tempDir, "mcp-prompts")
		_ = os.MkdirAll(promptsDir, 0755)
		_ = os.WriteFile(filepath.Join(promptsDir, "invalid_fm.md"), []byte("---\n: broken\n---\nHello"), 0644)

		cp := content.NewContentProvider(tempDir)
		defs, err := DiscoverPrompts(cp)
		assert.NoError(t, err)
		assert.Empty(t, defs)
	})

	t.Run("WalkDirError", func(t *testing.T) {
		tempDir := t.TempDir()
		promptsDir := filepath.Join(tempDir, "mcp-prompts")
		_ = os.MkdirAll(promptsDir, 0755)

		// Create a subdirectory and make it unreadable
		subDir := filepath.Join(promptsDir, "unreadable")
		_ = os.MkdirAll(subDir, 0000)
		defer func() { _ = os.Chmod(subDir, 0755) }() // cleanup so TempDir can delete it

		cp := content.NewContentProvider(tempDir)
		_, err := DiscoverPrompts(cp)
		assert.NoError(t, err) // Should continue walking and not return error
	})

	t.Run("StatError", func(t *testing.T) {
		tempDir := t.TempDir()
		cp := content.NewContentProvider(tempDir)
		// Use a path that is a file to trigger Stat error? No, Stat works on files.
		// Use a path that is inside a non-existent directory with no permissions?
		badPath := filepath.Join(tempDir, "unreadable_dir", "prompts")
		_ = os.MkdirAll(filepath.Join(tempDir, "unreadable_dir"), 0000)
		defer func() { _ = os.Chmod(filepath.Join(tempDir, "unreadable_dir"), 0755) }()

		cp.PromptsDir = badPath
		_, err := DiscoverPrompts(cp)
		assert.Error(t, err)
	})
}

func TestPromptProvider_GetPrompt(t *testing.T) {
	tempDir := t.TempDir()
	cp := content.NewContentProvider(tempDir)
	promptsDir := filepath.Join(tempDir, "mcp-prompts")
	_ = os.MkdirAll(promptsDir, 0755)

	t.Run("Success", func(t *testing.T) {
		md := "---\nname: test\ndescription: d\n---\nHello {{.name}}"
		_ = os.WriteFile(filepath.Join(promptsDir, "s.md"), []byte(md), 0644)
		defs, _ := DiscoverPrompts(cp)
		p := NewPromptProvider(defs, cp)

		messages, err := p.GetPrompt("test", map[string]string{"name": "World"})
		assert.NoError(t, err)
		assert.Len(t, messages, 1)
		assert.Equal(t, "Hello World", messages[0].Content.(*mcp.TextContent).Text)
	})

	t.Run("RequiredArgumentMissing", func(t *testing.T) {
		md := `---
name: req
description: d
arguments:
  - name: arg1
    required: true
---
Hello`
		_ = os.WriteFile(filepath.Join(promptsDir, "req.md"), []byte(md), 0644)
		defs, _ := DiscoverPrompts(cp)
		p := NewPromptProvider(defs, cp)

		_, err := p.GetPrompt("req", map[string]string{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "missing required argument: arg1")
	})

	t.Run("RequiredArgumentEmpty", func(t *testing.T) {
		md := `---
name: req-empty
description: d
arguments:
  - name: arg1
    required: true
---
Hello`
		_ = os.WriteFile(filepath.Join(promptsDir, "req_empty.md"), []byte(md), 0644)
		defs, _ := DiscoverPrompts(cp)
		p := NewPromptProvider(defs, cp)

		_, err := p.GetPrompt("req-empty", map[string]string{"arg1": ""})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "missing required argument: arg1")
	})

	t.Run("OptionalArgumentMissing", func(t *testing.T) {
		md := "---\nname: optional-arg\ndescription: d\n---\nHello {{.missing}}"
		_ = os.WriteFile(filepath.Join(promptsDir, "opt.md"), []byte(md), 0644)
		defs, _ := DiscoverPrompts(cp)
		p := NewPromptProvider(defs, cp)

		messages, err := p.GetPrompt("optional-arg", map[string]string{})
		assert.NoError(t, err)
		assert.Len(t, messages, 1)
		// "missing" key resolves to empty string, so "Hello "
		assert.Equal(t, "Hello ", messages[0].Content.(*mcp.TextContent).Text)
	})

	t.Run("UnknownPrompt", func(t *testing.T) {
		p := NewPromptProvider(nil, cp)
		_, err := p.GetPrompt("unknown", nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unknown prompt")
	})
}

func TestPromptProvider_ListPrompts(t *testing.T) {
	defs := []PromptDefinition{
		{
			Name:        "p1",
			Description: "d1",
			Arguments: []PromptArgument{
				{Name: "a1", Description: "ad1", Required: true},
			},
		},
	}
	p := NewPromptProvider(defs, nil)
	list := p.ListPrompts()
	assert.Len(t, list, 1)
	assert.Equal(t, "p1", list[0].Name)
	assert.Equal(t, "a1", list[0].Arguments[0].Name)
}
