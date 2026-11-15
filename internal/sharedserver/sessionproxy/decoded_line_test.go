package sessionproxy

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDecodeLine_ReadsTheTopLevelMembers(t *testing.T) {
	line := []byte(`{"jsonrpc":"2.0","id":"a","method":"tools/call","params":{"x":[1]},"result":{"y":2}}` + "\n")

	decoded := decodeLine(line)

	assert.Equal(t, line, decoded.raw)
	assert.True(t, decoded.object)
	assert.True(t, decoded.hasMethod)
	assert.True(t, decoded.methodIs("tools/call"))
	assert.Equal(t, json.RawMessage(`"a"`), decoded.id)
	assert.Equal(t, json.RawMessage(`{"x":[1]}`), decoded.params)
	assert.Equal(t, json.RawMessage(`{"y":2}`), decoded.result)
}

func TestDecodeLine_LeavesEveryMemberEmptyForANonObject(t *testing.T) {
	for _, line := range []string{"not json\n", "null\n", "[]\n", `"text"` + "\n", `{"id":1` + "\n"} {
		t.Run(line, func(t *testing.T) {
			decoded := decodeLine([]byte(line))

			assert.Equal(t, decodedLine{raw: []byte(line)}, decoded)
		})
	}
}

func TestDecodeLine_RecordsAMethodMemberThatIsNotAString(t *testing.T) {
	for _, method := range []string{"null", "1", "{}", "[]"} {
		t.Run(method, func(t *testing.T) {
			decoded := decodeLine([]byte(`{"id":1,"method":` + method + `}` + "\n"))

			assert.True(t, decoded.hasMethod)
			assert.False(t, decoded.methodIsString)
			assert.False(t, decoded.methodIs(""))
		})
	}
}

func TestDecodeLine_MatchesMemberNamesCaseSensitively(t *testing.T) {
	decoded := decodeLine([]byte(`{"ID":1,"Method":"x","Params":{}}` + "\n"))

	assert.True(t, decoded.object)
	assert.False(t, decoded.hasMethod)
	assert.Nil(t, decoded.id)
	assert.Nil(t, decoded.params)
}
