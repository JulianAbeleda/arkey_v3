package gpu

import (
	"context"
	"os"
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
}
type Result struct {
	Vendor Vendor
	Name   string
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
				return Result{NVIDIA, name}, nil
			}
		}
	}
	if _, e := os.Stat(k); e == nil {
		if b, e := d.Runner.Run(ctx, "rocminfo"); e == nil {
			for _, line := range strings.Split(string(b), "\n") {
				if x := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "Marketing Name:")); x != "" && strings.Contains(line, "Marketing Name:") {
					return Result{AMD, x}, nil
				}
			}
			return Result{AMD, "AMD GPU"}, nil
		}
		if _, e := d.Runner.Run(ctx, "rocm-smi"); e == nil {
			return Result{AMD, "AMD GPU"}, nil
		}
	}
	return Result{Unknown, "No supported GPU"}, nil
}
