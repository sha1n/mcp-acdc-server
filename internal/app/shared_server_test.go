package app

import (
	"context"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sha1n/mcp-acdc-server/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sharedServerCreateSpy(created *bool) func(context.Context, *config.Settings, string) (Components, error) {
	return func(context.Context, *config.Settings, string) (Components, error) {
		*created = true
		return Components{MCP: mcp.NewServer(&mcp.Implementation{Name: "test", Version: "1.0"}, nil)}, nil
	}
}

func TestRunSharedServer_RequiresAnEndpointDirectory(t *testing.T) {
	created := false
	params := RunParams{CreateServer: sharedServerCreateSpy(&created)}

	err := runSharedServer(context.Background(), params, &config.Settings{}, sharedServerFlagTestIdentity, "test")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "shared server requires an endpoint directory")
	assert.False(t, created, "no server may be built without an endpoint directory")
}

func TestRunSharedServer_EndpointDirectoryResolutionFailureIsReturned(t *testing.T) {
	errResolve := errors.New("no home directory")
	created := false
	params := RunParams{
		CreateServer: sharedServerCreateSpy(&created),
		EndpointDir:  func() (string, error) { return "", errResolve },
	}

	err := runSharedServer(context.Background(), params, &config.Settings{}, sharedServerFlagTestIdentity, "test")

	require.ErrorIs(t, err, errResolve)
	assert.Contains(t, err.Error(), "failed to resolve endpoint directory")
	assert.False(t, created, "no server may be built without an endpoint directory")
}
