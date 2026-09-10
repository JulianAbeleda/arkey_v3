package models

import "testing"

// hybridGGUF mirrors Qwen3.6-27B: 65 blocks, every 4th an attention layer.
func hybridGGUF() GGUF {
	return GGUF{Architecture: "qwen35", BlockCount: 65, HeadCount: 24, HeadCountKV: 4,
		KeyLength: 256, ValueLength: 256, EmbeddingLength: 5120, ContextLength: 262144,
		FullAttentionInterval: 4}
}

func TestDeriveContextSizeUsesFullNativeWindowWithQuantisedCache(t *testing.T) {
	// 32,607 MiB card, 16.74 GiB model. q8_0 leaves room for the whole window.
	got := DeriveContextSize(hybridGGUF(), 17968619520, 34189869056, 1, false)
	if got != 262144 {
		t.Fatalf("q8_0 window = %d, want the model's native 262144", got)
	}
}

func TestDeriveContextSizeClampsToVRAMAtF16(t *testing.T) {
	got := DeriveContextSize(hybridGGUF(), 17968619520, 34189869056, 2, false)
	if got >= 262144 || got < 131072 {
		t.Fatalf("f16 window = %d, want a VRAM-limited value below the native max", got)
	}
	if got%contextGranularity != 0 {
		t.Fatalf("window %d is not %d-aligned", got, contextGranularity)
	}
}

func TestDeriveContextSizeNeverExceedsModelContextLength(t *testing.T) {
	g := hybridGGUF()
	g.ContextLength = 32768
	if got := DeriveContextSize(g, 17968619520, 34189869056, 1, false); got != 32768 {
		t.Fatalf("window = %d, want clamp to the model's 32768", got)
	}
}

func TestDeriveContextSizeReturnsZeroWhenInputsUnknown(t *testing.T) {
	g := hybridGGUF()
	for _, tc := range []struct {
		name        string
		model, vram int64
		bytes       int
		blank       bool
	}{
		{name: "unknown vram", model: 17968619520, vram: 0, bytes: 1},
		{name: "unknown model size", model: 0, vram: 34189869056, bytes: 1},
		{name: "unreadable metadata", model: 17968619520, vram: 34189869056, bytes: 1, blank: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := g
			if tc.blank {
				in = GGUF{}
			}
			if got := DeriveContextSize(in, tc.model, tc.vram, tc.bytes, false); got != 0 {
				t.Fatalf("got %d, want 0 so the caller falls back", got)
			}
		})
	}
}

func TestDeriveContextSizeReturnsZeroWhenCardTooSmall(t *testing.T) {
	// 8 GiB card cannot hold a 16.74 GiB model at all.
	if got := DeriveContextSize(hybridGGUF(), 17968619520, 8589934592, 1, false); got != 0 {
		t.Fatalf("got %d, want 0 rather than a nonsense window", got)
	}
}

func TestDeriveContextSizeRejectsWindowsBelowTheFloor(t *testing.T) {
	g := hybridGGUF()
	g.ContextLength = 262144
	// Just enough VRAM for the model plus overhead, leaving a sliver for KV.
	vram := int64(17968619520) + runtimeOverheadBytes + safetyMarginBytes + 64*1024*1024
	if got := DeriveContextSize(g, 17968619520, vram, 1, false); got != 0 {
		t.Fatalf("got %d, want 0 because it is below MinDerivedContext", got)
	}
}

// denseGGUF mirrors Qwen3-8B: 36 dense attention layers, 8 KV heads, 128-wide
// heads. Its KV cost is about 72 KiB per token at q8_0, which is what makes a
// 32768 window cost 2.25 GiB on top of the weights.
func denseGGUF() GGUF {
	return GGUF{Architecture: "qwen3", BlockCount: 36, HeadCount: 32, HeadCountKV: 8,
		KeyLength: 128, ValueLength: 128, EmbeddingLength: 4096, ContextLength: 40960}
}

// A 16 GiB Apple Silicon Mac is the machine this rule exists for. Measured
// there on 2026-09-10 with this model: a 32768 window wired 12.54 GiB and left
// 60 MB free, at which point CoreAudio could not allocate a stream buffer and
// every spoken reply was synthesised and silently dropped. A 16384 window
// wired 11.33 GiB, left 699 MB, and audio worked.
func TestDeriveContextSizeLeavesUnifiedMemoryForTheRestOfTheMachine(t *testing.T) {
	const (
		sixteenGiB = 16 << 30
		modelBytes = 5027783488 // Qwen3-8B-Q4_K_M, the real file on Julian's Mac
	)
	unified := DeriveContextSize(denseGGUF(), modelBytes, sixteenGiB, 1, true)
	discrete := DeriveContextSize(denseGGUF(), modelBytes, sixteenGiB, 1, false)
	if unified >= discrete {
		t.Fatalf("unified window %d is not smaller than the discrete one %d; the "+
			"whole point is that the same byte count is shared with the OS", unified, discrete)
	}
	// The measured boundary: 32768 starved the machine, 16384 did not.
	if unified > 16384 {
		t.Fatalf("unified window = %d, want no more than the 16384 that was "+
			"measured to leave the machine usable", unified)
	}
	if unified < MinDerivedContext {
		t.Fatalf("unified window = %d, below the %d floor; a machine that cannot "+
			"reach it should fall back rather than run crippled", unified, MinDerivedContext)
	}
}

// The ceiling is on the model's whole footprint, so a model too large for the
// share leaves nothing for a window and the caller falls back rather than
// running a machine into the ground.
func TestDeriveContextSizeRefusesWhenTheModelAloneExceedsTheUnifiedShare(t *testing.T) {
	const eightGiB = 8 << 30
	if got := DeriveContextSize(denseGGUF(), 5027783488, eightGiB, 1, true); got != 0 {
		t.Fatalf("unified window = %d, want 0 so the configured value is used", got)
	}
}
