package gpu

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type Vendor string

const (
	Unknown Vendor = "unknown"
	NVIDIA  Vendor = "nvidia"
	AMD     Vendor = "amd"
	Metal   Vendor = "metal"
)

type Runner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}
type Detector struct {
	Runner             Runner
	NVIDIAControl, KFD string
	SysfsRoot          string
}
type Result struct {
	Vendor         Vendor
	Name           string
	TotalVRAMBytes int64
	// Unified is true when TotalVRAMBytes is the machine's whole memory,
	// shared with the operating system and every other application, rather
	// than a dedicated pool. Apple Silicon is the case that matters: a
	// budget that would be safe on a discrete card starves the desktop here.
	Unified bool
	// Count is the number of GPUs found; DeviceVRAMBytes holds each one's
	// memory where it could be read. TotalVRAMBytes stays the first device's
	// figure so single-GPU sizing is unchanged; PooledVRAMBytes covers a model
	// split across several devices.
	Count           int
	DeviceVRAMBytes []int64
	// Arch is the first GPU's ISA where the driver names one: an AMD
	// gfx target such as gfx1201 (RDNA 4) or gfx942 (CDNA 3).
	Arch string
}

// PooledVRAMBytes is the memory n devices offer to one model split evenly
// across them: the smallest of the first n devices, n times. A split puts an
// equal share on each device, so the smallest one is the binding limit.
func (r Result) PooledVRAMBytes(n int) int64 {
	if n <= 1 || len(r.DeviceVRAMBytes) < n {
		return r.TotalVRAMBytes
	}
	smallest := r.DeviceVRAMBytes[0]
	for _, b := range r.DeviceVRAMBytes[1:n] {
		smallest = min(smallest, b)
	}
	return smallest * int64(n)
}

// parseIntLoose strips whitespace and common unit suffixes before parsing an int64.
func parseIntLoose(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, "MiB")
	s = strings.TrimSuffix(s, "MB")
	s = strings.TrimSuffix(s, "B")
	s = strings.TrimSpace(s)
	n, e := strconv.ParseInt(s, 10, 64)
	if e != nil {
		return 0, false
	}
	return n, true
}

func nvidiaVRAMBytes(ctx context.Context, r Runner) []int64 {
	b, e := r.Run(ctx, "nvidia-smi", "--query-gpu=memory.total", "--format=csv,noheader,nounits")
	if e != nil {
		return nil
	}
	var out []int64
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		mib, ok := parseIntLoose(line)
		if !ok {
			return nil
		}
		out = append(out, mib*1024*1024)
	}
	return out
}

func amdVRAMBytes(ctx context.Context, r Runner, sysfsRoot string) []int64 {
	if b, e := r.Run(ctx, "rocm-smi", "--showmeminfo", "vram", "--csv"); e == nil {
		if out := rocmSMITotals(string(b)); len(out) > 0 {
			return out
		}
	}
	if sysfsRoot == "" {
		sysfsRoot = "/sys/class/drm"
	}
	// Display-only adapters (a BMC's ASPEED, say) expose no VRAM file.
	matches, _ := filepath.Glob(filepath.Join(sysfsRoot, "card*", "device", "mem_info_vram_total"))
	sort.Strings(matches)
	var out []int64
	for _, m := range matches {
		b, e := os.ReadFile(m)
		if e != nil {
			continue
		}
		if n, ok := parseIntLoose(string(b)); ok && n > 0 {
			out = append(out, n)
		}
	}
	return out
}

// rocmSMITotals reads each card's total VRAM from `rocm-smi --showmeminfo vram
// --csv`. The table is "device,VRAM Total Memory (B),VRAM Total Used Memory (B)":
// the total is found by its header, never by position, because the last
// column is the memory in use.
func rocmSMITotals(out string) []int64 {
	col := -1
	var totals []int64
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(strings.TrimSpace(line), ",")
		if col < 0 {
			for i, f := range fields {
				h := strings.ToLower(f)
				if strings.Contains(h, "total") && !strings.Contains(h, "used") {
					col = i
				}
			}
			continue
		}
		if col < len(fields) {
			if n, ok := parseIntLoose(fields[col]); ok && n > 0 {
				totals = append(totals, n)
			}
		}
	}
	return totals
}

type rocmAgent struct{ name, arch string }

// rocminfoGPUs returns every GPU agent's marketing name and gfx target.
// rocminfo lists the CPU as an agent too, usually first, so only agents whose
// Device Type is GPU count. An agent's own Name (gfx1201) precedes its ISA
// entries, whose indented Name lines (amdgcn-amd-amdhsa--gfx1201) are skipped.
func rocminfoGPUs(out string) []rocmAgent {
	var gpus []rocmAgent
	cur := rocmAgent{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "Marketing Name:"); ok {
			cur.name = strings.TrimSpace(v)
		} else if v, ok := strings.CutPrefix(line, "Name:"); ok && strings.HasPrefix(strings.TrimSpace(v), "gfx") {
			cur = rocmAgent{arch: strings.TrimSpace(v)}
		} else if v, ok := strings.CutPrefix(line, "Device Type:"); ok && strings.TrimSpace(v) == "GPU" {
			gpus = append(gpus, cur)
		}
	}
	return gpus
}

func firstMarketingName(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "Marketing Name:"); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// discrete builds a Result for a vendor whose devices were enumerated.
func discrete(v Vendor, name string, count int, vram []int64) Result {
	count = max(count, len(vram), 1)
	var first int64
	if len(vram) > 0 {
		first = vram[0]
	}
	return Result{Vendor: v, Name: name, TotalVRAMBytes: first, Count: count, DeviceVRAMBytes: vram}
}

func (d Detector) Detect(ctx context.Context) (Result, error) {
	// Without a Runner nothing can be probed; a zero Detector must answer
	// rather than crash, whatever devices this machine happens to have.
	if d.Runner == nil {
		return Result{Vendor: Unknown, Name: "No supported GPU"}, nil
	}
	n := d.NVIDIAControl
	if n == "" {
		n = "/dev/nvidiactl"
	}
	k := d.KFD
	if k == "" {
		k = "/dev/kfd"
	}
	if _, e := os.Stat(n); e == nil {
		if b, e := d.Runner.Run(ctx, "nvidia-smi", "--query-gpu=name", "--format=csv,noheader"); e == nil {
			names := strings.Split(strings.TrimSpace(string(b)), "\n")
			if name := strings.TrimSpace(names[0]); name != "" {
				return discrete(NVIDIA, name, len(names), nvidiaVRAMBytes(ctx, d.Runner)), nil
			}
		}
	}
	if _, e := os.Stat(k); e == nil {
		if b, e := d.Runner.Run(ctx, "rocminfo"); e == nil {
			vram := amdVRAMBytes(ctx, d.Runner, d.SysfsRoot)
			if gpus := rocminfoGPUs(string(b)); len(gpus) > 0 && gpus[0].name != "" {
				r := discrete(AMD, gpus[0].name, len(gpus), vram)
				r.Arch = gpus[0].arch
				return r, nil
			}
			if x := firstMarketingName(string(b)); x != "" {
				return discrete(AMD, x, 0, vram), nil
			}
			return discrete(AMD, "AMD GPU", 0, vram), nil
		}
		if _, e := d.Runner.Run(ctx, "rocm-smi"); e == nil {
			return discrete(AMD, "AMD GPU", 0, amdVRAMBytes(ctx, d.Runner, d.SysfsRoot)), nil
		}
	}
	return detectPlatform(ctx, d.Runner)
}
