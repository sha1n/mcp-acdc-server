package integration

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/tests/integration/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestToolsListIntegration verifies that tools/list returns search and read tools
// with correct schemas (P-TOOL-01, P-TOOL-02)
func TestToolsListIntegration(t *testing.T) {
	client := testkit.NewStdioTestClient(t, &testkit.ContentDirOptions{
		Resources: map[string]string{
			"test.md": "---\nname: Test Resource\ndescription: A test resource\n---\nTest content.",
		},
	})
	defer client.Close()

	ctx := context.Background()

	// List tools
	result, err := client.ListTools(ctx)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Build map of tools by name
	toolNames := make(map[string]struct{})
	for _, tool := range result.Tools {
		toolNames[tool.Name] = struct{}{}
		assert.NotEmpty(t, tool.Description, "tool %s should have description", tool.Name)
		assert.NotNil(t, tool.InputSchema, "tool %s should have input schema", tool.Name)
	}

	assert.Contains(t, toolNames, "search", "should have search tool")
	assert.Contains(t, toolNames, "read", "should have read tool")
}

// TestSearchToolExecution tests search tool via tools/call (TOOL-01, TOOL-02)
func TestSearchToolExecution(t *testing.T) {
	client := testkit.NewStdioTestClient(t, &testkit.ContentDirOptions{
		Resources: map[string]string{
			"auth-guide.md": `---
name: Authentication Guide
description: Guide for authentication
keywords:
  - auth
  - security
---
This document covers authentication and security best practices.`,
		},
	})
	defer client.Close()

	ctx := context.Background()

	t.Run("search with results", func(t *testing.T) {
		result, err := client.CallTool(ctx, "search", map[string]any{
			"query": "authentication",
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotEmpty(t, result.Content, "search should return content")

		// Get text from first content item
		text := getTextContent(t, result)
		assert.Contains(t, text, "Authentication Guide", "search results should contain matching resource")
		assert.Contains(t, text, "acdc://", "search results should contain URI")
	})

	t.Run("search with no results", func(t *testing.T) {
		result, err := client.CallTool(ctx, "search", map[string]any{
			"query": "nonexistentterm12345",
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotEmpty(t, result.Content)

		text := getTextContent(t, result)
		assert.Contains(t, text, "No results", "should indicate no results found")
	})
}

// TestReadToolExecution tests read tool via tools/call (TOOL-03, TOOL-04)
func TestReadToolExecution(t *testing.T) {
	client := testkit.NewStdioTestClient(t, &testkit.ContentDirOptions{
		Resources: map[string]string{
			"api-reference.md": `---
name: API Reference
description: API documentation
---
# API Reference

This is the API documentation content.`,
		},
	})
	defer client.Close()

	ctx := context.Background()

	t.Run("read with valid URI", func(t *testing.T) {
		result, err := client.CallTool(ctx, "read", map[string]any{
			"uri": "acdc://api-reference",
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotEmpty(t, result.Content)

		text := getTextContent(t, result)
		assert.Contains(t, text, "API Reference", "should contain resource content")
		assert.Contains(t, text, "API documentation content", "should contain body content")
		assert.NotContains(t, text, "---", "should strip frontmatter")
	})

	t.Run("read with invalid URI", func(t *testing.T) {
		result, err := client.CallTool(ctx, "read", map[string]any{
			"uri": "acdc://nonexistent-resource",
		})

		// Read tool should return an error for invalid URI
		// The error might come as an RPC error or as isError in the result
		if err != nil {
			return // Error properly returned
		}

		// If no error, check for isError flag in result
		if result != nil && result.IsError {
			return // Error properly indicated
		}

		t.Error("read tool should indicate error for invalid URI")
	})
}

// getTextContent extracts text from the first content item in a tool result
func getTextContent(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	require.NotEmpty(t, result.Content, "result should have content")

	// Content items implement the Content interface
	// We need to type assert to get the text
	for _, c := range result.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	t.Fatal("no text content found in result")
	return ""
}
