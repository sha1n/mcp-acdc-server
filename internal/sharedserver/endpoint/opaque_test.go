package endpoint_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/sha1n/mcp-acdc-server/internal/sharedserver/endpoint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testIdentity = "0123456789abcdef0123456789abcdef"

func assertYieldsNoPaths(t *testing.T, ep endpoint.Endpoint) {
	t.Helper()
	socketPath, socketErr := ep.SocketPath(testIdentity)
	lockPath, lockErr := ep.LockPath(testIdentity)

	assert.Error(t, socketErr)
	assert.Empty(t, socketPath)
	assert.Error(t, lockErr)
	assert.Empty(t, lockPath)
}

func TestEndpoint_HasNoFieldOutsideThePackageCanSet(t *testing.T) {
	typ := reflect.TypeFor[endpoint.Endpoint]()

	for i := range typ.NumField() {
		assert.False(t, typ.Field(i).IsExported(), "field %s lets a caller build an unverified endpoint", typ.Field(i).Name)
	}
}

func TestEndpoint_ZeroValueFromOutsideThePackageYieldsNoPaths(t *testing.T) {
	assertYieldsNoPaths(t, endpoint.Endpoint{})
}

func TestOpenEndpoint_FailureYieldsNoPaths(t *testing.T) {
	notADir := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(notADir, nil, 0o600))

	tests := []struct {
		name string
		dir  string
	}{
		{name: "relative", dir: "runtime/acdc-mcp"},
		{name: "abstract socket prefix", dir: "@runtime"},
		{name: "not a directory", dir: notADir},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Chdir(t.TempDir())

			ep, err := endpoint.OpenEndpoint(tt.dir)

			require.Error(t, err)
			assertYieldsNoPaths(t, ep)
		})
	}
}
