package mcp

import (
	"context"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/sha1n/mcp-acdc-server-go/internal/resources"
)

func TestGetResourceToolHandler_Errors(t *testing.T) {
	provider := resources.NewResourceProvider(nil)
	handler := NewGetResourceToolHandler(provider)

	t.Run("MissingArguments", func(t *testing.T) {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name:      "get_resource",
				Arguments: map[string]interface{}{},
			},
		}
		_, err := handler(context.Background(), req)
		if err == nil {
			t.Error("expected error for missing 'uri' argument")
		}
	})

	t.Run("UnknownResource", func(t *testing.T) {
		req := mcp.CallToolRequest{
			Params: mcp.CallToolParams{
				Name: "get_resource",
				Arguments: map[string]interface{}{
					"uri": "acdc://unknown",
				},
			},
		}
		_, err := handler(context.Background(), req)
		if err == nil {
			t.Error("expected error for unknown resource")
		}
	})
}
