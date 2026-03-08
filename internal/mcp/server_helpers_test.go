package mcp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"text/template"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/content"
	"github.com/sha1n/mcp-acdc-server/internal/prompts"
	"github.com/sha1n/mcp-acdc-server/internal/resources"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMakeResourceHandler_Success(t *testing.T) {
	// Create temp file with markdown content
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "test-resource.md")
	resourceContent := "---\nname: Test Resource\ndescription: A test\n---\n# Test Content\n\nThis is test content."
	err := os.WriteFile(filePath, []byte(resourceContent), 0644)
	require.NoError(t, err)

	resourceProvider := resources.NewResourceProvider([]resources.ResourceDefinition{
		{
			Name:        "Test Resource",
			URI:         "acdc://test-resource",
			Description: "A test resource",
			MIMEType:    "text/markdown",
			FilePath:    filePath,
		},
	})

	handler := makeResourceHandler(resourceProvider, "acdc://test-resource")
	require.NotNil(t, handler)

	ctx := context.Background()
	req := &mcp.ReadResourceRequest{
		Params: &mcp.ReadResourceParams{
			URI: "acdc://test-resource",
		},
	}

	result, err := handler(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Contents, 1)
	assert.Equal(t, "acdc://test-resource", result.Contents[0].URI)
	assert.Equal(t, "text/markdown", result.Contents[0].MIMEType)
	assert.Equal(t, "# Test Content\n\nThis is test content.", result.Contents[0].Text)
}

func TestMakeResourceHandler_Error_NotFound(t *testing.T) {
	resourceProvider := resources.NewResourceProvider([]resources.ResourceDefinition{})

	handler := makeResourceHandler(resourceProvider, "acdc://nonexistent")
	require.NotNil(t, handler)

	ctx := context.Background()
	req := &mcp.ReadResourceRequest{
		Params: &mcp.ReadResourceParams{
			URI: "acdc://nonexistent",
		},
	}

	result, err := handler(ctx, req)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "resource")
	assert.Nil(t, result)
}

func TestMakePromptHandler_Success(t *testing.T) {
	tempDir := t.TempDir()
	contentProvider := content.NewContentProvider(tempDir)

	tmpl, err := template.New("test-prompt").Parse("Hello {{.name}}!")
	require.NoError(t, err)

	promptProvider := prompts.NewPromptProvider([]prompts.PromptDefinition{
		{
			Name:        "test-prompt",
			Description: "A test prompt",
			Arguments: []prompts.PromptArgument{
				{
					Name:        "name",
					Description: "User name",
					Required:    true,
				},
			},
			Template: tmpl,
		},
	}, contentProvider)

	handler := makePromptHandler(promptProvider, "test-prompt")
	require.NotNil(t, handler)

	ctx := context.Background()
	req := &mcp.GetPromptRequest{
		Params: &mcp.GetPromptParams{
			Name: "test-prompt",
			Arguments: map[string]string{
				"name": "Alice",
			},
		},
	}

	result, err := handler(ctx, req)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "Rendered prompt: test-prompt", result.Description)
	require.Len(t, result.Messages, 1)
	assert.Equal(t, mcp.Role("user"), result.Messages[0].Role)

	textContent, ok := result.Messages[0].Content.(*mcp.TextContent)
	require.True(t, ok)
	assert.Equal(t, "Hello Alice!", textContent.Text)
}

func TestMakePromptHandler_Error_PromptNotFound(t *testing.T) {
	tempDir := t.TempDir()
	contentProvider := content.NewContentProvider(tempDir)

	promptProvider := prompts.NewPromptProvider([]prompts.PromptDefinition{}, contentProvider)

	handler := makePromptHandler(promptProvider, "nonexistent-prompt")
	require.NotNil(t, handler)

	ctx := context.Background()
	req := &mcp.GetPromptRequest{
		Params: &mcp.GetPromptParams{
			Name:      "nonexistent-prompt",
			Arguments: map[string]string{},
		},
	}

	result, err := handler(ctx, req)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "prompt")
	assert.Nil(t, result)
}

func TestMakePromptHandler_Error_MissingRequiredArgument(t *testing.T) {
	tempDir := t.TempDir()
	contentProvider := content.NewContentProvider(tempDir)

	tmpl, err := template.New("test-prompt").Parse("Value: {{.required_arg}}")
	require.NoError(t, err)

	promptProvider := prompts.NewPromptProvider([]prompts.PromptDefinition{
		{
			Name:        "test-prompt",
			Description: "A test prompt",
			Arguments: []prompts.PromptArgument{
				{
					Name:        "required_arg",
					Description: "A required argument",
					Required:    true,
				},
			},
			Template: tmpl,
		},
	}, contentProvider)

	handler := makePromptHandler(promptProvider, "test-prompt")
	require.NotNil(t, handler)

	ctx := context.Background()
	req := &mcp.GetPromptRequest{
		Params: &mcp.GetPromptParams{
			Name:      "test-prompt",
			Arguments: map[string]string{}, // Missing required argument
		},
	}

	result, err := handler(ctx, req)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "required")
	assert.Nil(t, result)
}
