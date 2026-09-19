package model2vec

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeSafetensors(t *testing.T, tensors map[string]any, payload []byte) string {
	t.Helper()
	header, err := json.Marshal(tensors)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := binary.Write(&buf, binary.LittleEndian, uint64(len(header))); err != nil {
		t.Fatal(err)
	}
	buf.Write(header)
	buf.Write(payload)
	path := filepath.Join(t.TempDir(), "model.safetensors")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func float32Payload(values ...float32) []byte {
	payload := make([]byte, 4*len(values))
	for i, v := range values {
		binary.LittleEndian.PutUint32(payload[4*i:], math.Float32bits(v))
	}
	return payload
}

func TestReadMatrix_ReadsRowMajorFloat32(t *testing.T) {
	path := writeSafetensors(t,
		map[string]any{"embeddings": map[string]any{
			"dtype":        "F32",
			"shape":        []int{2, 3},
			"data_offsets": []int{0, 24},
		}},
		float32Payload(1, 2, 3, 4, 5, 6))

	m, err := readMatrix(path, "embeddings")
	if err != nil {
		t.Fatalf("readMatrix: %v", err)
	}
	if m.rows != 2 || m.cols != 3 {
		t.Fatalf("got %dx%d, want 2x3", m.rows, m.cols)
	}
	if got := m.row(1); !slices.Equal(got, []float32{4, 5, 6}) {
		t.Fatalf("row(1) = %v, want [4 5 6]", got)
	}
}

func TestReadMatrix_RejectsUnusableTensors(t *testing.T) {
	payload := float32Payload(1, 2, 3, 4, 5, 6)
	cases := map[string]struct {
		tensors map[string]any
		want    string
	}{
		"missing tensor": {
			tensors: map[string]any{"other": map[string]any{"dtype": "F32", "shape": []int{2, 3}, "data_offsets": []int{0, 24}}},
			want:    `tensor "embeddings" not found`,
		},
		"unsupported dtype": {
			tensors: map[string]any{"embeddings": map[string]any{"dtype": "F16", "shape": []int{2, 3}, "data_offsets": []int{0, 24}}},
			want:    "dtype F16",
		},
		"wrong rank": {
			tensors: map[string]any{"embeddings": map[string]any{"dtype": "F32", "shape": []int{6}, "data_offsets": []int{0, 24}}},
			want:    "2-dimensional",
		},
		"declared size disagrees with the shape": {
			tensors: map[string]any{"embeddings": map[string]any{"dtype": "F32", "shape": []int{2, 3}, "data_offsets": []int{0, 48}}},
			want:    "want 24",
		},
		"extra tensor": {
			tensors: map[string]any{
				"embeddings": map[string]any{"dtype": "F32", "shape": []int{2, 3}, "data_offsets": []int{0, 24}},
				"weights":    map[string]any{"dtype": "F32", "shape": []int{2}, "data_offsets": []int{24, 32}},
			},
			want: "unsupported tensor",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := readMatrix(writeSafetensors(t, tc.tensors, payload), "embeddings")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

func TestReadMatrix_ReportsATruncatedFile(t *testing.T) {
	path := writeSafetensors(t,
		map[string]any{"embeddings": map[string]any{
			"dtype": "F32", "shape": []int{2, 3}, "data_offsets": []int{0, 24}}},
		float32Payload(1, 2, 3))

	_, err := readMatrix(path, "embeddings")
	if err == nil || !strings.Contains(err.Error(), "unexpected EOF") {
		t.Fatalf("got %v, want an unexpected-EOF error", err)
	}
}
