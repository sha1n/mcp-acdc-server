package mcp

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/sha1n/mcp-acdc-server-go/internal/resources"
)

func makeResourceHandler(resourceProvider *resources.ResourceProvider, uri string) func(context.Context, mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	return func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		content, err := resourceProvider.ReadResource(uri)
		if err != nil {
			return nil, err
		}
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      uri,
				MIMEType: "text/markdown",
				Text:     content,
			},
		}, nil
	}
}
