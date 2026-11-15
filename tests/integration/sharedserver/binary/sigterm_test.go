//go:build unix

package binary

import (
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// The terminated run stops while its session is still attached and long
// before its linger, and its lock passes to the recovery's run: it neither
// waited for the session nor for the linger.
func TestBinary_ASIGTERMedSharedServerExitsAtOnceAndTheSessionRecovers(t *testing.T) {
	deadline := time.Now().Add(binaryTestBudget)
	s := startAttachedSession(t, deadline)

	s.signalServer(t, syscall.SIGTERM)

	terminated, _ := s.requireNextRequestRecovers(t, deadline)
	assert.False(t, terminated.Stopped.IsZero(), "a SIGTERMed shared server writes its stopped entry")
	assert.True(t, terminated.LingerExpired.IsZero(), "the run stopped on the signal, not on its linger")
	assert.True(t, slices.Contains(terminated.Sessions, "attached:"+s.session))
	endSession(t, s.launch, deadline)
}
