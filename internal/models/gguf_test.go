package models

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// TestMain canonicalizes TMPDIR so t.TempDir() yields symlink-free paths. On
// macOS the system temp dir resolves through /tmp -> /private/tmp (and /var ->
// /private/var), which the path-security guard rejects; real config/state
// directories under the user's home are not symlinked, so this only affects
// tests. It is a no-op where TMPDIR is already canonical (Linux).
func TestMain(m *testing.M) {
	if resolved, err := filepath.EvalSymlinks(os.TempDir()); err == nil {
		os.Setenv("TMPDIR", resolved)
	}
	os.Exit(m.Run())
}

// ggufBuilder builds a synthetic GGUF header into a bytes.Buffer for tests.
type ggufBuilder struct {
	buf     bytes.Buffer
	entries int
}

func newGGUFBuilder(version uint32, tensorCount uint64) *ggufBuilder {
	b := &ggufBuilder{}
	b.buf.WriteString("GGUF")
	binary.Write(&b.buf, binary.LittleEndian, version)
	binary.Write(&b.buf, binary.LittleEndian, tensorCount)
	// placeholder for kv count, patched in bytes() via kvCountOffset
	binary.Write(&b.buf, binary.LittleEndian, uint64(0))
	return b
}

func (b *ggufBuilder) writeKey(key string) {
	binary.Write(&b.buf, binary.LittleEndian, uint64(len(key)))
	b.buf.WriteString(key)
}

func (b *ggufBuilder) addString(key, val string) {
	b.writeKey(key)
	binary.Write(&b.buf, binary.LittleEndian, uint32(ggufTypeString))
	binary.Write(&b.buf, binary.LittleEndian, uint64(len(val)))
	b.buf.WriteString(val)
	b.entries++
}

func (b *ggufBuilder) addUint32(key string, val uint32) {
	b.writeKey(key)
	binary.Write(&b.buf, binary.LittleEndian, uint32(ggufTypeUint32))
	binary.Write(&b.buf, binary.LittleEndian, val)
	b.entries++
}

func (b *ggufBuilder) addUint64(key string, val uint64) {
	b.writeKey(key)
	binary.Write(&b.buf, binary.LittleEndian, uint32(ggufTypeUint64))
	binary.Write(&b.buf, binary.LittleEndian, val)
	b.entries++
}

// addUint32Array writes an array-of-uint32 value, to exercise array skipping.
func (b *ggufBuilder) addUint32Array(key string, vals []uint32) {
	b.writeKey(key)
	binary.Write(&b.buf, binary.LittleEndian, uint32(ggufTypeArray))
	binary.Write(&b.buf, binary.LittleEndian, uint32(ggufTypeUint32))
	binary.Write(&b.buf, binary.LittleEndian, uint64(len(vals)))
	for _, v := range vals {
		binary.Write(&b.buf, binary.LittleEndian, v)
	}
	b.entries++
}

// bytes returns the final header bytes with the kv count patched in.
func (b *ggufBuilder) bytes() []byte {
	out := b.buf.Bytes()
	// magic(4) + version(4) + tensor_count(8) + kv_count(8)
	binary.LittleEndian.PutUint64(out[16:24], uint64(b.entries))
	return out
}

func writeTempGGUF(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "model.gguf")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadGGUF_Basic(t *testing.T) {
	b := newGGUFBuilder(3, 10)
	b.addString("general.architecture", "qwen3")
	b.addUint32("qwen3.block_count", 32)
	b.addUint32("qwen3.attention.head_count", 16)
	b.addUint32("qwen3.attention.head_count_kv", 4)
	b.addUint32("qwen3.attention.key_length", 128)
	b.addUint32("qwen3.attention.value_length", 128)
	b.addUint32("qwen3.embedding_length", 2048)
	b.addUint32("qwen3.context_length", 32768)

	path := writeTempGGUF(t, b.bytes())
	g, err := ReadGGUF(path)
	if err != nil {
		t.Fatalf("ReadGGUF: %v", err)
	}
	if g.Architecture != "qwen3" {
		t.Errorf("Architecture = %q, want qwen3", g.Architecture)
	}
	if g.BlockCount != 32 || g.HeadCount != 16 || g.HeadCountKV != 4 {
		t.Errorf("counts = %+v", g)
	}
	if g.KeyLength != 128 || g.ValueLength != 128 {
		t.Errorf("key/value length = %+v", g)
	}
	if g.EmbeddingLength != 2048 || g.ContextLength != 32768 {
		t.Errorf("embedding/context = %+v", g)
	}
}

func TestReadGGUF_HeadDimFallback(t *testing.T) {
	b := newGGUFBuilder(3, 0)
	b.addString("general.architecture", "qwen3")
	b.addUint32("qwen3.block_count", 28)
	b.addUint32("qwen3.attention.head_count", 8)
	b.addUint32("qwen3.attention.head_count_kv", 2)
	b.addUint32("qwen3.embedding_length", 1024) // no key_length present

	path := writeTempGGUF(t, b.bytes())
	g, err := ReadGGUF(path)
	if err != nil {
		t.Fatalf("ReadGGUF: %v", err)
	}
	if g.KeyLength != 0 {
		t.Fatalf("KeyLength = %d, want 0 (absent)", g.KeyLength)
	}
	// headDim fallback = embedding_length / head_count = 1024/8 = 128
	got := g.KVBytesPerToken(2)
	want := int64(2 * 28 * 2 * 128 * 2)
	if got != want {
		t.Errorf("KVBytesPerToken(2) = %d, want %d", got, want)
	}
}

func TestReadGGUF_Hybrid(t *testing.T) {
	b := newGGUFBuilder(3, 0)
	b.addString("general.architecture", "qwen35")
	b.addUint32("qwen35.block_count", 65)
	b.addUint32("qwen35.attention.head_count", 24)
	b.addUint32("qwen35.attention.head_count_kv", 4)
	b.addUint32("qwen35.attention.key_length", 256)
	b.addUint32("qwen35.attention.value_length", 256)
	b.addUint32("qwen35.full_attention_interval", 4)
	b.addUint32("qwen35.ssm.conv_kernel", 4)
	b.addUint32("qwen35.ssm.state_size", 128)
	b.addUint32("qwen35.ssm.group_count", 16)
	b.addUint32("qwen35.ssm.time_step_rank", 48)
	b.addUint32("qwen35.ssm.inner_size", 6144)

	path := writeTempGGUF(t, b.bytes())
	g, err := ReadGGUF(path)
	if err != nil {
		t.Fatalf("ReadGGUF: %v", err)
	}
	if !g.IsHybrid() {
		t.Error("IsHybrid() = false, want true")
	}
	if got := g.AttentionLayers(); got != 16 {
		t.Errorf("AttentionLayers() = %d, want 16", got)
	}
	if got, want := g.KVBytesPerToken(2), int64(65536); got != want {
		t.Errorf("KVBytesPerToken(2) = %d, want %d", got, want)
	}
	if got, want := g.KVBytesPerToken(1), int64(32768); got != want {
		t.Errorf("KVBytesPerToken(1) = %d, want %d", got, want)
	}
}

func TestReadGGUF_PureAttentionUnchanged(t *testing.T) {
	b := newGGUFBuilder(3, 0)
	b.addString("general.architecture", "qwen3")
	b.addUint32("qwen3.block_count", 32)
	b.addUint32("qwen3.attention.head_count", 16)
	b.addUint32("qwen3.attention.head_count_kv", 4)
	b.addUint32("qwen3.attention.key_length", 128)
	// no full_attention_interval, no ssm.* keys

	path := writeTempGGUF(t, b.bytes())
	g, err := ReadGGUF(path)
	if err != nil {
		t.Fatalf("ReadGGUF: %v", err)
	}
	if g.IsHybrid() {
		t.Error("IsHybrid() = true, want false")
	}
	if got := g.AttentionLayers(); got != g.BlockCount {
		t.Errorf("AttentionLayers() = %d, want BlockCount %d", got, g.BlockCount)
	}
	want := int64(2 * 32 * 4 * 128 * 2)
	if got := g.KVBytesPerToken(2); got != want {
		t.Errorf("KVBytesPerToken(2) = %d, want %d", got, want)
	}
}

func TestReadGGUF_ArraySkipping(t *testing.T) {
	b := newGGUFBuilder(3, 0)
	b.addString("general.architecture", "qwen3")
	b.addUint32Array("qwen3.some_array", []uint32{1, 2, 3, 4, 5})
	b.addUint32("qwen3.block_count", 40)
	b.addUint32("qwen3.attention.head_count", 16)
	b.addUint32("qwen3.attention.head_count_kv", 16)
	b.addUint32("qwen3.attention.key_length", 256)
	b.addUint32("qwen3.context_length", 8192)

	path := writeTempGGUF(t, b.bytes())
	g, err := ReadGGUF(path)
	if err != nil {
		t.Fatalf("ReadGGUF: %v", err)
	}
	if g.BlockCount != 40 || g.HeadCountKV != 16 || g.KeyLength != 256 || g.ContextLength != 8192 {
		t.Errorf("keys after array not aligned correctly: %+v", g)
	}
}

func TestReadGGUF_BadMagic(t *testing.T) {
	data := []byte("NOPE\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00")
	path := writeTempGGUF(t, data)
	if _, err := ReadGGUF(path); err == nil {
		t.Fatal("expected error for bad magic, got nil")
	}
}

func TestReadGGUF_BadVersion(t *testing.T) {
	b := newGGUFBuilder(99, 0)
	path := writeTempGGUF(t, b.bytes())
	if _, err := ReadGGUF(path); err == nil {
		t.Fatal("expected error for unsupported version, got nil")
	}
}

func TestKVBytesPerToken(t *testing.T) {
	g := GGUF{
		BlockCount:  32,
		HeadCountKV: 4,
		KeyLength:   128,
	}
	// 2 * 32 * 4 * 128 * bytesPerElement
	if got, want := g.KVBytesPerToken(2), int64(2*32*4*128*2); got != want {
		t.Errorf("f16: got %d, want %d", got, want)
	}
	if got, want := g.KVBytesPerToken(1), int64(2*32*4*128*1); got != want {
		t.Errorf("q8_0: got %d, want %d", got, want)
	}
}

func TestKVBytesPerToken_Insufficient(t *testing.T) {
	g := GGUF{}
	if got := g.KVBytesPerToken(2); got != 0 {
		t.Errorf("expected 0 for empty GGUF, got %d", got)
	}
}

const realModelPath = "/home/ubuntu/models/Qwen3.6-27B-Q4_K_M.gguf"

func TestReadGGUF_RealModel(t *testing.T) {
	if _, err := os.Stat(realModelPath); err != nil {
		t.Skipf("real model not present: %v", err)
	}
	g, err := ReadGGUF(realModelPath)
	if err != nil {
		t.Fatalf("ReadGGUF(%s): %v", realModelPath, err)
	}
	t.Logf("parsed: %+v", g)
	if g.Architecture == "" {
		t.Error("Architecture is empty")
	}
	if g.BlockCount <= 0 {
		t.Error("BlockCount <= 0")
	}
	if g.HeadCount <= 0 {
		t.Error("HeadCount <= 0")
	}
	if g.HeadCountKV <= 0 {
		t.Error("HeadCountKV <= 0")
	}
	if g.ContextLength <= 0 {
		t.Error("ContextLength <= 0")
	}

	// Qwen3.6-27B is a hybrid SSM(Mamba)/attention model: only every 4th
	// layer is full attention, the rest are recurrent SSM layers with
	// constant-size state. Confirm the header reflects that and that
	// KVBytesPerToken accounts for it rather than over-counting by ~4x.
	if !g.IsHybrid() {
		t.Fatal("IsHybrid() = false, want true for Qwen3.6")
	}
	if g.FullAttentionInterval != 4 {
		t.Errorf("FullAttentionInterval = %d, want 4", g.FullAttentionInterval)
	}
	if got := g.AttentionLayers(); got != 16 {
		t.Errorf("AttentionLayers() = %d, want 16", got)
	}

	f16 := g.KVBytesPerToken(2)
	q8 := g.KVBytesPerToken(1)
	t.Logf("KVBytesPerToken f16=%d q8_0=%d", f16, q8)
	if got, want := f16, int64(65536); got != want {
		t.Errorf("KVBytesPerToken(2) = %d, want %d", got, want)
	}
	if f16 != q8*2 {
		t.Errorf("f16 (%d) should be exactly 2x q8_0 (%d)", f16, q8)
	}
}
