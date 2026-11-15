package testkit

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func procargs2(argc uint32, rest string) []byte {
	return append(binary.NativeEndian.AppendUint32(nil, argc), rest...)
}

func TestParseProcArgs2_ReadsArgvAfterTheExecPathAndItsPadding(t *testing.T) {
	got, err := parseProcArgs2(procargs2(2, "/bin/x\x00\x00\x00\x00x\x00-a\x00HOME=/h\x00"))

	require.NoError(t, err)
	assert.Equal(t, []string{"x", "-a"}, got)
}

func TestParseProcArgs2_RejectsAMalformedBuffer(t *testing.T) {
	for name, buf := range map[string][]byte{
		"shorter than argc":         {1, 0},
		"no exec path terminator":   procargs2(1, "/bin/x"),
		"fewer arguments than argc": procargs2(3, "/bin/x\x00x\x00-a\x00"),
		"an unterminated argument":  procargs2(1, "/bin/x\x00x"),
		"no argument at all":        procargs2(0, "/bin/x\x00"),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := parseProcArgs2(buf)

			assert.Error(t, err)
		})
	}
}
