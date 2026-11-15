//go:build unix

package binary

import (
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestBinary_AfterASIGKILLOfTheSharedServerTheSessionsNextRequestSucceedsOnANewServer(t *testing.T) {
	deadline := time.Now().Add(binaryTestBudget)
	s := startAttachedSession(t, deadline)

	s.signalServer(t, syscall.SIGKILL)

	crashed, _ := s.requireNextRequestRecovers(t, deadline)
	assert.True(t, crashed.Stopped.IsZero(), "a killed shared server writes no stopped entry")
	endSession(t, s.launch, deadline)
}
