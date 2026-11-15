//go:build unix

package sessionproxy

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The recovery is driven directly, so its connect wait is the only one that
// runs: no first connect has to beat a short wait before the outage.
func TestRecover_ConnectWaitExpiryFailsTheRecoveryAndAnswersEveryHeldRequest(t *testing.T) {
	parent, err := os.MkdirTemp("/tmp", "acdc-recover-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(parent) })
	endpointDir := filepath.Join(parent, "ep")
	var stdout bytes.Buffer
	s := &session{
		cfg: Config{
			EndpointDir: func() (string, error) { return endpointDir, nil },
			Identity:    func() (string, error) { return "0123456789abcdef0123456789abcdef", nil },
			ConnectWait: 300 * time.Millisecond,
			Spawn:       func(string) error { return nil },
			Logger:      slog.New(slog.DiscardHandler),
			Stdout:      &stdout,
		},
		input: &hostInput{
			held: [][]byte{
				[]byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n"),
				[]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"),
				[]byte(`{"jsonrpc":"2.0","id":"two","method":"ping"}` + "\n"),
			},
			ended:   make(chan struct{}),
			stopped: make(chan struct{}),
		},
		ledger: newLedger(),
	}

	att, err := s.recover(context.Background(), &connectionLost{err: io.EOF})

	assert.Nil(t, att)
	var failure *recoveryFailure
	require.ErrorAs(t, err, &failure)
	assert.Contains(t, err.Error(), "failed to recover the shared server connection (connect_wait): ")
	assert.ErrorIs(t, err, ErrConnectWaitExpired)
	assert.Equal(t, crashErrorLine("1")+crashErrorLine(`"two"`), stdout.String(),
		"every held request is answered in held order, and nothing else is written")
}
