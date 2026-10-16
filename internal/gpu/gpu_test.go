package gpu

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fake struct {
	out string
	err error
}

func (f fake) Run(context.Context, string, ...string) ([]byte, error) { return []byte(f.out), f.err }
func TestBackend(t *testing.T) {
	b, e := LDDInspector{fake{"libggml-cuda.so", nil}}.Backend(context.Background(), "x")
	if e != nil || b != CUDABackend {
		t.Fatal(b, e)
	}
}

// argRouter returns a different response depending on whether the command
// args contain a given substring, so name and memory queries can be told apart.
type argRouter struct {
	byArg map[string]fake
	def   fake
}

func (r argRouter) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	joined := name + " " + strings.Join(args, " ")
	for k, f := range r.byArg {
		if strings.Contains(joined, k) {
			return f.Run(ctx, name, args...)
		}
	}
	return r.def.Run(ctx, name, args...)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(path), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, []byte(content), 0644); e != nil {
		t.Fatal(e)
	}
}

func TestDetectNVIDIAVRAM(t *testing.T) {
	nvidiactl := filepath.Join(t.TempDir(), "nvidiactl")
	writeFile(t, nvidiactl, "")
	r := argRouter{byArg: map[string]fake{
		"query-gpu=name":         {"GeForce RTX 4090\n", nil},
		"query-gpu=memory.total": {"24564\n", nil},
	}}
	d := Detector{Runner: r, NVIDIAControl: nvidiactl, KFD: filepath.Join(t.TempDir(), "nokfd")}
	res, e := d.Detect(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if res.Vendor != NVIDIA || res.Name != "GeForce RTX 4090" {
		t.Fatal(res)
	}
	want := int64(24564) * 1024 * 1024
	if res.TotalVRAMBytes != want {
		t.Fatalf("got %d want %d", res.TotalVRAMBytes, want)
	}
}

func TestDetectNVIDIAVRAMMultiGPU(t *testing.T) {
	nvidiactl := filepath.Join(t.TempDir(), "nvidiactl")
	writeFile(t, nvidiactl, "")
	r := argRouter{byArg: map[string]fake{
		"query-gpu=name":         {"GeForce RTX 4090\n", nil},
		"query-gpu=memory.total": {"24564\n16384\n", nil},
	}}
	d := Detector{Runner: r, NVIDIAControl: nvidiactl, KFD: filepath.Join(t.TempDir(), "nokfd")}
	res, e := d.Detect(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	want := int64(24564) * 1024 * 1024
	if res.TotalVRAMBytes != want {
		t.Fatalf("got %d want %d", res.TotalVRAMBytes, want)
	}
}

func TestDetectNVIDIAVRAMUnparseable(t *testing.T) {
	nvidiactl := filepath.Join(t.TempDir(), "nvidiactl")
	writeFile(t, nvidiactl, "")
	r := argRouter{byArg: map[string]fake{
		"query-gpu=name":         {"GeForce RTX 4090\n", nil},
		"query-gpu=memory.total": {"", os.ErrNotExist},
	}}
	d := Detector{Runner: r, NVIDIAControl: nvidiactl, KFD: filepath.Join(t.TempDir(), "nokfd")}
	res, e := d.Detect(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if res.Vendor != NVIDIA || res.Name != "GeForce RTX 4090" {
		t.Fatal(res)
	}
	if res.TotalVRAMBytes != 0 {
		t.Fatalf("want 0, got %d", res.TotalVRAMBytes)
	}
}

func TestDetectAMDSysfsFallback(t *testing.T) {
	kfd := filepath.Join(t.TempDir(), "kfd")
	writeFile(t, kfd, "")
	sysfsRoot := t.TempDir()
	writeFile(t, filepath.Join(sysfsRoot, "card0", "device", "mem_info_vram_total"), "8589934592")
	// rocminfo succeeds with a marketing name; the same fixed text is also
	// what rocm-smi's vram query would return here, which is unparseable as
	// a number, so amdVRAMBytes must fall back to the sysfs root.
	r := fake{"Marketing Name: Test GPU\n", nil}
	d := Detector{Runner: r, NVIDIAControl: filepath.Join(t.TempDir(), "nonvidia"), KFD: kfd, SysfsRoot: sysfsRoot}
	res, e := d.Detect(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if res.Vendor != AMD || res.Name != "Test GPU" {
		t.Fatal(res)
	}
	if res.TotalVRAMBytes != 8589934592 {
		t.Fatalf("got %d", res.TotalVRAMBytes)
	}
}

// rocminfo lists the CPU as the first agent. The GPU name must come from a
// GPU agent, and every GPU agent counts toward the device total.
func TestDetectAMDCountsGPUAgentsAndSkipsCPU(t *testing.T) {
	kfd := filepath.Join(t.TempDir(), "kfd")
	writeFile(t, kfd, "")
	sysfsRoot := t.TempDir()
	writeFile(t, filepath.Join(sysfsRoot, "card2", "device", "mem_info_vram_total"), "34208743424")
	writeFile(t, filepath.Join(sysfsRoot, "card3", "device", "mem_info_vram_total"), "34208743424")
	// A display-only adapter has a card directory but no VRAM file.
	writeFile(t, filepath.Join(sysfsRoot, "card1", "device", "vendor"), "0x1a03")
	rocminfo := `Agent 1
  Name:                    AMD Ryzen Threadripper PRO 9955WX 16-Cores
  Marketing Name:          AMD Ryzen Threadripper PRO 9955WX 16-Cores
  Device Type:             CPU
Agent 2
  Name:                    gfx1201
  Marketing Name:          AMD Radeon AI PRO R9700
  Device Type:             GPU
Agent 3
  Name:                    gfx1201
  Marketing Name:          AMD Radeon AI PRO R9700
  Device Type:             GPU
`
	r := argRouter{byArg: map[string]fake{"rocminfo": {rocminfo, nil}}, def: fake{"", os.ErrNotExist}}
	d := Detector{Runner: r, NVIDIAControl: filepath.Join(t.TempDir(), "nonvidia"), KFD: kfd, SysfsRoot: sysfsRoot}
	res, e := d.Detect(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if res.Vendor != AMD || res.Name != "AMD Radeon AI PRO R9700" || res.Count != 2 || res.Arch != "gfx1201" {
		t.Fatalf("%+v", res)
	}
	if res.TotalVRAMBytes != 34208743424 || res.PooledVRAMBytes(2) != 2*34208743424 {
		t.Fatalf("total %d pooled %d", res.TotalVRAMBytes, res.PooledVRAMBytes(2))
	}
}

// Mixed cards pool at the smallest card's size: an even split fills the
// smallest one first.
func TestPooledVRAMUsesSmallestDevice(t *testing.T) {
	nvidiactl := filepath.Join(t.TempDir(), "nvidiactl")
	writeFile(t, nvidiactl, "")
	r := argRouter{byArg: map[string]fake{
		"query-gpu=name":         {"GeForce RTX 4090\nGeForce RTX 4080\n", nil},
		"query-gpu=memory.total": {"24564\n16384\n", nil},
	}}
	d := Detector{Runner: r, NVIDIAControl: nvidiactl, KFD: filepath.Join(t.TempDir(), "nokfd")}
	res, e := d.Detect(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	mib := int64(1024 * 1024)
	if res.Count != 2 || res.PooledVRAMBytes(1) != 24564*mib || res.PooledVRAMBytes(2) != 2*16384*mib {
		t.Fatalf("%+v pooled1 %d pooled2 %d", res, res.PooledVRAMBytes(1), res.PooledVRAMBytes(2))
	}
}
