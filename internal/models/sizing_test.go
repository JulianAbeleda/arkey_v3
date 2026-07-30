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
	got := DeriveContextSize(hybridGGUF(), 17968619520, 34189869056, 1)
	if got != 262144 {
		t.Fatalf("q8_0 window = %d, want the model's native 262144", got)
	}
}

func TestDeriveContextSizeClampsToVRAMAtF16(t *testing.T) {
	got := DeriveContextSize(hybridGGUF(), 17968619520, 34189869056, 2)
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
	if got := DeriveContextSize(g, 17968619520, 34189869056, 1); got != 32768 {
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
			if got := DeriveContextSize(in, tc.model, tc.vram, tc.bytes); got != 0 {
				t.Fatalf("got %d, want 0 so the caller falls back", got)
			}
		})
	}
}

func TestDeriveContextSizeReturnsZeroWhenCardTooSmall(t *testing.T) {
	// 8 GiB card cannot hold a 16.74 GiB model at all.
	if got := DeriveContextSize(hybridGGUF(), 17968619520, 8589934592, 1); got != 0 {
		t.Fatalf("got %d, want 0 rather than a nonsense window", got)
	}
}

func TestDeriveContextSizeRejectsWindowsBelowTheFloor(t *testing.T) {
	g := hybridGGUF()
	g.ContextLength = 262144
	// Just enough VRAM for the model plus overhead, leaving a sliver for KV.
	vram := int64(17968619520) + runtimeOverheadBytes + safetyMarginBytes + 64*1024*1024
	if got := DeriveContextSize(g, 17968619520, vram, 1); got != 0 {
		t.Fatalf("got %d, want 0 because it is below MinDerivedContext", got)
	}
}
