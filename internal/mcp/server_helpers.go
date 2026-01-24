package mcp

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/prompts"
	"github.com/sha1n/mcp-acdc-server/internal/resources"
)

func makeResourceHandler(resourceProvider *resources.ResourceProvider, uri string) mcp.ResourceHandler {
	return func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		slog.Info("Resource request", "uri", uri)
		content, err := resourceProvider.ReadResource(uri)
		if err != nil {
			slog.Error("Resource read failed", "uri", uri, "error", err)
			return nil, err
		}
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{{
				URI:      uri,
				MIMEType: "text/markdown",
				Text:     content,
			}},
		}, nil
	}
}

func makePromptHandler(promptProvider *prompts.PromptProvider, name string) mcp.PromptHandler {
	return func(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		slog.Info("Prompt request", "name", name)
		messages, err := promptProvider.GetPrompt(name, req.Params.Arguments)
		if err != nil {
			slog.Error("Prompt retrieval failed", "name", name, "error", err)
			return nil, err
		}
		return &mcp.GetPromptResult{
			Description: fmt.Sprintf("Rendered prompt: %s", name),
			Messages:    messages,
		}, nil
	}
}
