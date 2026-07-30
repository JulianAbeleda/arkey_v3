package gpu

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Vendor string

const (
	Unknown Vendor = "unknown"
	NVIDIA  Vendor = "nvidia"
	AMD     Vendor = "amd"
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

func nvidiaVRAMBytes(ctx context.Context, r Runner) int64 {
	b, e := r.Run(ctx, "nvidia-smi", "--query-gpu=memory.total", "--format=csv,noheader,nounits")
	if e != nil {
		return 0
	}
	line := strings.Split(string(b), "\n")[0]
	mib, ok := parseIntLoose(line)
	if !ok {
		return 0
	}
	return mib * 1024 * 1024
}

func amdVRAMBytes(ctx context.Context, r Runner, sysfsRoot string) int64 {
	if b, e := r.Run(ctx, "rocm-smi", "--showmeminfo", "vram", "--csv"); e == nil {
		for _, line := range strings.Split(string(b), "\n") {
			fields := strings.Split(strings.TrimSpace(line), ",")
			last := strings.TrimSpace(fields[len(fields)-1])
			if n, ok := parseIntLoose(last); ok && n > 0 {
				return n
			}
		}
	}
	if sysfsRoot == "" {
		sysfsRoot = "/sys/class/drm"
	}
	matches, _ := filepath.Glob(filepath.Join(sysfsRoot, "card*", "device", "mem_info_vram_total"))
	var best int64
	for _, m := range matches {
		b, e := os.ReadFile(m)
		if e != nil {
			continue
		}
		if n, ok := parseIntLoose(string(b)); ok && n > best {
			best = n
		}
	}
	return best
}

func (d Detector) Detect(ctx context.Context) (Result, error) {
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
			name := strings.TrimSpace(strings.Split(string(b), "\n")[0])
			if name != "" {
				return Result{NVIDIA, name, nvidiaVRAMBytes(ctx, d.Runner)}, nil
			}
		}
	}
	if _, e := os.Stat(k); e == nil {
		if b, e := d.Runner.Run(ctx, "rocminfo"); e == nil {
			for _, line := range strings.Split(string(b), "\n") {
				if x := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "Marketing Name:")); x != "" && strings.Contains(line, "Marketing Name:") {
					return Result{AMD, x, amdVRAMBytes(ctx, d.Runner, d.SysfsRoot)}, nil
				}
			}
			return Result{AMD, "AMD GPU", amdVRAMBytes(ctx, d.Runner, d.SysfsRoot)}, nil
		}
		if _, e := d.Runner.Run(ctx, "rocm-smi"); e == nil {
			return Result{AMD, "AMD GPU", amdVRAMBytes(ctx, d.Runner, d.SysfsRoot)}, nil
		}
	}
	return Result{Unknown, "No supported GPU", 0}, nil
}
