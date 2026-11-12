package testkit

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

type fatalRecorder struct {
	testing.TB
	fatal string
}

func (r *fatalRecorder) Fatalf(format string, args ...any) {
	r.fatal = fmt.Sprintf(format, args...)
	runtime.Goexit()
}

func TestStartSSE_StartupFailureIsFatal(t *testing.T) {
	recorder := &fatalRecorder{TB: t}
	returned := false
	done := make(chan struct{})

	go func() {
		defer close(done)
		StartSSE(recorder, nil, &FlagOptions{AuthType: "unsupported"})
		returned = true
	}()
	<-done

	require.False(t, returned)
	require.Contains(t, recorder.fatal, "Failed to start SSE server")
}
