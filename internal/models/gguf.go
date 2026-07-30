package models

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

// GGUF holds the subset of a GGUF file's header metadata needed to compute
// KV-cache cost per token.
type GGUF struct {
	Architecture    string
	BlockCount      int
	HeadCount       int
	HeadCountKV     int
	KeyLength       int
	ValueLength     int
	EmbeddingLength int
	ContextLength   int

	// Hybrid SSM(Mamba)/attention fields. All optional; 0 means absent.
	FullAttentionInterval int
	SSMConvKernel         int
	SSMStateSize          int
	SSMGroupCount         int
	SSMTimeStepRank       int
	SSMInnerSize          int
}

const (
	ggufMaxStringLen = 1 << 20 // 1MB: reject absurd key/string lengths
	ggufMaxKVCount   = 1 << 20 // reject absurd metadata kv counts
	ggufMaxArrayLen  = 1 << 24 // reject absurd array element counts
)

// gguf value types, per the GGUF spec.
const (
	ggufTypeUint8   = 0
	ggufTypeInt8    = 1
	ggufTypeUint16  = 2
	ggufTypeInt16   = 3
	ggufTypeUint32  = 4
	ggufTypeInt32   = 5
	ggufTypeFloat32 = 6
	ggufTypeBool    = 7
	ggufTypeString  = 8
	ggufTypeArray   = 9
	ggufTypeUint64  = 10
	ggufTypeInt64   = 11
	ggufTypeFloat64 = 12
)

// ReadGGUF parses only the header (magic, version, counts, metadata KV
// section) of the GGUF file at path. It never reads tensor data, so it is
// safe to use on multi-gigabyte model files.
func ReadGGUF(path string) (GGUF, error) {
	f, err := os.Open(path)
	if err != nil {
		return GGUF{}, err
	}
	defer f.Close()

	r := bufio.NewReaderSize(f, 64<<10)

	magic := make([]byte, 4)
	if _, err := io.ReadFull(r, magic); err != nil {
		return GGUF{}, fmt.Errorf("gguf: reading magic: %w", err)
	}
	if string(magic) != "GGUF" {
		return GGUF{}, fmt.Errorf("gguf: not a GGUF file (bad magic %q)", magic)
	}

	var version uint32
	if err := binary.Read(r, binary.LittleEndian, &version); err != nil {
		return GGUF{}, fmt.Errorf("gguf: reading version: %w", err)
	}
	if version != 2 && version != 3 {
		return GGUF{}, fmt.Errorf("gguf: unsupported version %d (expected 2 or 3)", version)
	}

	var tensorCount, kvCount uint64
	if err := binary.Read(r, binary.LittleEndian, &tensorCount); err != nil {
		return GGUF{}, fmt.Errorf("gguf: reading tensor_count: %w", err)
	}
	if err := binary.Read(r, binary.LittleEndian, &kvCount); err != nil {
		return GGUF{}, fmt.Errorf("gguf: reading metadata_kv_count: %w", err)
	}
	if kvCount > ggufMaxKVCount {
		return GGUF{}, fmt.Errorf("gguf: metadata_kv_count %d exceeds sanity limit", kvCount)
	}

	kv := make(map[string]any, kvCount)
	for i := uint64(0); i < kvCount; i++ {
		key, err := readGGUFString(r)
		if err != nil {
			return GGUF{}, fmt.Errorf("gguf: reading key %d: %w", i, err)
		}
		val, err := readGGUFValue(r)
		if err != nil {
			return GGUF{}, fmt.Errorf("gguf: reading value for key %q: %w", key, err)
		}
		kv[key] = val
	}

	g := GGUF{}
	arch, _ := kv["general.architecture"].(string)
	g.Architecture = arch

	g.BlockCount = kvInt(kv, arch+".block_count")
	g.HeadCount = kvInt(kv, arch+".attention.head_count")
	g.HeadCountKV = kvInt(kv, arch+".attention.head_count_kv")
	g.KeyLength = kvInt(kv, arch+".attention.key_length")
	g.ValueLength = kvInt(kv, arch+".attention.value_length")
	g.EmbeddingLength = kvInt(kv, arch+".embedding_length")
	g.ContextLength = kvInt(kv, arch+".context_length")

	g.FullAttentionInterval = kvInt(kv, arch+".full_attention_interval")
	g.SSMConvKernel = kvInt(kv, arch+".ssm.conv_kernel")
	g.SSMStateSize = kvInt(kv, arch+".ssm.state_size")
	g.SSMGroupCount = kvInt(kv, arch+".ssm.group_count")
	g.SSMTimeStepRank = kvInt(kv, arch+".ssm.time_step_rank")
	g.SSMInnerSize = kvInt(kv, arch+".ssm.inner_size")

	return g, nil
}

// kvInt extracts an integer-ish metadata value, tolerating any of the
// numeric GGUF value types.
func kvInt(kv map[string]any, key string) int {
	switch v := kv[key].(type) {
	case uint64:
		return int(v)
	case int64:
		return int(v)
	case uint32:
		return int(v)
	case int32:
		return int(v)
	case uint16:
		return int(v)
	case int16:
		return int(v)
	case uint8:
		return int(v)
	case int8:
		return int(v)
	case float32:
		return int(v)
	case float64:
		return int(v)
	}
	return 0
}

func readGGUFString(r *bufio.Reader) (string, error) {
	var length uint64
	if err := binary.Read(r, binary.LittleEndian, &length); err != nil {
		return "", err
	}
	if length > ggufMaxStringLen {
		return "", fmt.Errorf("string length %d exceeds sanity limit", length)
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

// readGGUFValue reads a single typed value (the value_type tag plus the
// value itself), returning it as the closest matching Go type. Arrays are
// read recursively and returned as []any so nested structures are fully
// consumed and subsequent keys stay aligned.
func readGGUFValue(r *bufio.Reader) (any, error) {
	var valType uint32
	if err := binary.Read(r, binary.LittleEndian, &valType); err != nil {
		return nil, err
	}
	return readGGUFTypedValue(r, valType)
}

func readGGUFTypedValue(r *bufio.Reader, valType uint32) (any, error) {
	switch valType {
	case ggufTypeUint8:
		var v uint8
		err := binary.Read(r, binary.LittleEndian, &v)
		return v, err
	case ggufTypeInt8:
		var v int8
		err := binary.Read(r, binary.LittleEndian, &v)
		return v, err
	case ggufTypeUint16:
		var v uint16
		err := binary.Read(r, binary.LittleEndian, &v)
		return v, err
	case ggufTypeInt16:
		var v int16
		err := binary.Read(r, binary.LittleEndian, &v)
		return v, err
	case ggufTypeUint32:
		var v uint32
		err := binary.Read(r, binary.LittleEndian, &v)
		return v, err
	case ggufTypeInt32:
		var v int32
		err := binary.Read(r, binary.LittleEndian, &v)
		return v, err
	case ggufTypeFloat32:
		var v float32
		err := binary.Read(r, binary.LittleEndian, &v)
		return v, err
	case ggufTypeBool:
		var v uint8
		err := binary.Read(r, binary.LittleEndian, &v)
		return v != 0, err
	case ggufTypeString:
		return readGGUFString(r)
	case ggufTypeUint64:
		var v uint64
		err := binary.Read(r, binary.LittleEndian, &v)
		return v, err
	case ggufTypeInt64:
		var v int64
		err := binary.Read(r, binary.LittleEndian, &v)
		return v, err
	case ggufTypeFloat64:
		var v float64
		err := binary.Read(r, binary.LittleEndian, &v)
		return v, err
	case ggufTypeArray:
		var elemType uint32
		if err := binary.Read(r, binary.LittleEndian, &elemType); err != nil {
			return nil, err
		}
		var count uint64
		if err := binary.Read(r, binary.LittleEndian, &count); err != nil {
			return nil, err
		}
		if count > ggufMaxArrayLen {
			return nil, fmt.Errorf("array length %d exceeds sanity limit", count)
		}
		cap := count
		if cap > 1024 {
			cap = 1024
		}
		out := make([]any, 0, cap)
		for i := uint64(0); i < count; i++ {
			v, err := readGGUFTypedValue(r, elemType)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	default:
		return nil, errors.New("unknown gguf value type")
	}
}

// AttentionLayers returns the number of layers that hold a growing,
// context-length-scaled KV cache. For a hybrid SSM(Mamba)/attention model
// (FullAttentionInterval > 1), only every Nth layer is full attention — the
// rest are recurrent SSM layers with constant-size state — so this is
// floor(BlockCount / FullAttentionInterval), at least 1. For a pure
// attention model (FullAttentionInterval absent or <= 1) this is just
// BlockCount, unchanged from prior behavior.
func (g GGUF) AttentionLayers() int {
	if g.FullAttentionInterval > 1 {
		n := g.BlockCount / g.FullAttentionInterval
		if n < 1 {
			n = 1
		}
		return n
	}
	return g.BlockCount
}

// IsHybrid reports whether the model interleaves recurrent SSM (Mamba)
// layers with attention layers, as opposed to being pure attention.
func (g GGUF) IsHybrid() bool {
	return g.FullAttentionInterval > 1 ||
		g.SSMConvKernel != 0 || g.SSMStateSize != 0 || g.SSMGroupCount != 0 ||
		g.SSMTimeStepRank != 0 || g.SSMInnerSize != 0
}

// KVBytesPerToken returns the per-token KV cache cost in bytes for the
// given bytes-per-element (2 for f16, 1 for q8_0). It returns 0 if the
// header did not carry enough information to compute this — callers should
// treat 0 as "unknown", not as a real zero cost.
//
// For a hybrid SSM(Mamba)/attention model, the recurrent layers hold
// constant-size state that does not scale with context length, so only the
// attention layers (see AttentionLayers) contribute per-token KV growth;
// using the full block count there would overcount KV several-fold.
func (g GGUF) KVBytesPerToken(bytesPerElement int) int64 {
	attnLayers := g.AttentionLayers()
	if attnLayers <= 0 || g.HeadCountKV <= 0 || bytesPerElement <= 0 {
		return 0
	}
	headDim := g.KeyLength
	if headDim <= 0 {
		if g.HeadCount <= 0 || g.EmbeddingLength <= 0 {
			return 0
		}
		headDim = g.EmbeddingLength / g.HeadCount
	}
	if headDim <= 0 {
		return 0
	}
	return 2 * int64(attnLayers) * int64(g.HeadCountKV) * int64(headDim) * int64(bytesPerElement)
}
