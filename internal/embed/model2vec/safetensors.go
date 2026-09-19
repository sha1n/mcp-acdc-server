package model2vec

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
)

// tensorHeader is one entry of the safetensors JSON header.
type tensorHeader struct {
	DType       string `json:"dtype"`
	Shape       []int  `json:"shape"`
	DataOffsets []int  `json:"data_offsets"`
}

type matrix struct {
	rows int
	cols int
	data []float32
}

const (
	headerLengthBytes = 8
	// metadataKey is the one header key that is not a tensor.
	metadataKey     = "__metadata__"
	bytesPerFloat32 = 4
	// maxHeaderBytes bounds the header a file may declare, so a corrupt length
	// cannot make this allocate gigabytes before it fails.
	maxHeaderBytes = 100 << 20
	// decodeBlockBytes is the scratch buffer the tensor streams through. It
	// must be a multiple of bytesPerFloat32.
	decodeBlockBytes = 1 << 16
)

func readMatrix(path, tensor string) (*matrix, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open %q: %w", path, err)
	}
	defer func() { _ = file.Close() }()

	headers, dataStart, err := readSafetensorsHeader(file)
	if err != nil {
		return nil, fmt.Errorf("failed to parse %q: %w", path, err)
	}
	header, ok := headers[tensor]
	if !ok {
		return nil, fmt.Errorf("tensor %q not found in %q", tensor, path)
	}
	if err := rejectExtraTensors(headers, tensor, path); err != nil {
		return nil, err
	}
	rows, cols, start, length, err := tensorExtent(header)
	if err != nil {
		return nil, fmt.Errorf("tensor %q in %q: %w", tensor, path, err)
	}
	if _, err := file.Seek(dataStart+start, io.SeekStart); err != nil {
		return nil, fmt.Errorf("failed to seek to the tensor data in %q: %w", path, err)
	}
	values, err := decodeFloat32s(file, length/bytesPerFloat32)
	if err != nil {
		return nil, fmt.Errorf("failed to read the tensor data in %q: %w", path, err)
	}
	return &matrix{rows: rows, cols: cols, data: values}, nil
}

// rejectExtraTensors fails on any tensor this adapter does not pool, because a
// model that carries one does not match plain mean pooling.
func rejectExtraTensors(headers map[string]tensorHeader, tensor, path string) error {
	for name := range headers {
		if name != tensor && name != metadataKey {
			return fmt.Errorf("unsupported tensor %q in %q: this adapter implements plain mean pooling and would silently ignore it", name, path)
		}
	}
	return nil
}

func readSafetensorsHeader(r io.Reader) (map[string]tensorHeader, int64, error) {
	var length uint64
	if err := binary.Read(r, binary.LittleEndian, &length); err != nil {
		return nil, 0, fmt.Errorf("failed to read the header length: %w", err)
	}
	if length == 0 || length > maxHeaderBytes {
		return nil, 0, fmt.Errorf("header length %d is out of range", length)
	}
	raw := make([]byte, length)
	if _, err := io.ReadFull(r, raw); err != nil {
		return nil, 0, fmt.Errorf("failed to read the %d-byte header: %w", length, err)
	}
	var headers map[string]tensorHeader
	if err := json.Unmarshal(raw, &headers); err != nil {
		return nil, 0, fmt.Errorf("invalid header JSON: %w", err)
	}
	return headers, headerLengthBytes + int64(length), nil
}

// tensorExtent validates one header entry and returns the matrix shape plus
// the byte range of its data, relative to the end of the JSON header.
func tensorExtent(header tensorHeader) (rows, cols int, start, length int64, err error) {
	if header.DType != "F32" {
		return 0, 0, 0, 0, fmt.Errorf("dtype %s is not supported, want F32", header.DType)
	}
	if len(header.Shape) != 2 {
		return 0, 0, 0, 0, fmt.Errorf("tensor must be 2-dimensional, got shape %v", header.Shape)
	}
	rows, cols = header.Shape[0], header.Shape[1]
	if rows <= 0 || cols <= 0 {
		return 0, 0, 0, 0, fmt.Errorf("tensor shape %v has no elements", header.Shape)
	}
	if len(header.DataOffsets) != 2 {
		return 0, 0, 0, 0, fmt.Errorf("data_offsets %v must hold exactly two values", header.DataOffsets)
	}
	start, stop := int64(header.DataOffsets[0]), int64(header.DataOffsets[1])
	if start < 0 || stop < start {
		return 0, 0, 0, 0, fmt.Errorf("data_offsets %v are not a range", header.DataOffsets)
	}
	want := int64(rows) * int64(cols) * bytesPerFloat32
	if stop-start != want {
		return 0, 0, 0, 0, fmt.Errorf("data_offsets %v span %d bytes, want %d for shape %v", header.DataOffsets, stop-start, want, header.Shape)
	}
	return rows, cols, start, want, nil
}

// decodeFloat32s streams count little-endian float32s through a fixed scratch
// buffer. The returned slice is the only allocation proportional to the model.
func decodeFloat32s(r io.Reader, count int64) ([]float32, error) {
	values := make([]float32, count)
	block := make([]byte, decodeBlockBytes)
	for i := int64(0); i < count; {
		want := min(int64(len(block)), (count-i)*bytesPerFloat32)
		if _, err := io.ReadFull(r, block[:want]); err != nil {
			return nil, err
		}
		for j := int64(0); j < want; j += bytesPerFloat32 {
			values[i] = math.Float32frombits(binary.LittleEndian.Uint32(block[j:]))
			i++
		}
	}
	return values, nil
}

func (m *matrix) row(i int) []float32 {
	return m.data[i*m.cols : (i+1)*m.cols]
}
