package models

import "os"

// Context sizes are rounded down to a multiple of this so llama-server gets a
// clean batch-aligned value rather than an arbitrary token count.
const contextGranularity = 4096

// MinDerivedContext is the floor for a derived window. Below this a local model
// is not useful for agent work, and a machine that cannot fit it should fail
// loudly at startup rather than run a crippled session.
const MinDerivedContext = 8192

// runtimeOverheadBytes covers what sits in VRAM besides the weights and the
// per-token KV cache: llama.cpp compute buffers, and for hybrid models the
// recurrent SSM state, which is constant-size and therefore not part of
// KVBytesPerToken.
//
// Measured on Qwen3.6-27B-Q4_K_M (hybrid, 16 attention layers of 65): at 32768
// tokens the non-weight VRAM was 2,879 MiB against 2,048 MiB of attention KV,
// leaving ~831 MiB. Rounded up to 1 GiB. This is an empirical constant, not a
// derived quantity — GGUF metadata does not describe compute-buffer sizing.
const runtimeOverheadBytes int64 = 1 << 30

// safetyMarginBytes is held back for the display server, driver allocations and
// allocator fragmentation. Sizing to the last byte produces a model that loads
// on a headless box and OOMs on a desktop one.
const safetyMarginBytes int64 = 3 << 29 // 1.5 GiB

// DeriveContextSize computes the largest context length whose KV cache fits in
// VRAM alongside the model weights, clamped to what the model itself supports.
//
// It sizes against TOTAL VRAM rather than free VRAM deliberately. Free VRAM
// varies with whatever else is running, so a free-VRAM budget would hand the
// client a different window on every launch — and a client that compacted its
// history against last run's budget cannot safely resume against this one.
// Determinism per (GPU, model) pair is worth more than the reclaimed bytes.
//
// Returns 0 when the inputs are insufficient to compute an answer (unknown
// VRAM, unreadable metadata, or a machine too small to reach
// MinDerivedContext). Callers must treat 0 as "fall back to the configured
// value" rather than as a size.
func DeriveContextSize(g GGUF, modelBytes, totalVRAMBytes int64, bytesPerElement int) int {
	perToken := g.KVBytesPerToken(bytesPerElement)
	if perToken < 1 || modelBytes < 1 || totalVRAMBytes < 1 {
		return 0
	}
	budget := totalVRAMBytes - modelBytes - runtimeOverheadBytes - safetyMarginBytes
	if budget < 1 {
		return 0
	}
	tokens := budget / perToken
	if limit := int64(g.ContextLength); limit > 0 && tokens > limit {
		tokens = limit
	}
	tokens -= tokens % contextGranularity
	if tokens < MinDerivedContext {
		return 0
	}
	return int(tokens)
}

// DeriveContextSizeForModel reads the GGUF at path and derives a context size
// for it. Returns 0 if the file cannot be read or measured.
func DeriveContextSizeForModel(path string, totalVRAMBytes int64, bytesPerElement int) int {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return 0
	}
	g, err := ReadGGUF(path)
	if err != nil {
		return 0
	}
	return DeriveContextSize(g, info.Size(), totalVRAMBytes, bytesPerElement)
}
