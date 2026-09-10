package gpu

import (
	"context"
	"strings"
)

func detectPlatform(ctx context.Context, r Runner) (Result, error) {
	b, err := r.Run(ctx, "sysctl", "-n", "machdep.cpu.brand_string")
	name := strings.TrimSpace(string(b))
	if err != nil || !strings.HasPrefix(name, "Apple ") {
		return Result{Unknown, "No supported GPU", 0, false}, nil
	}
	// Unified memory is shared with the OS and applications, not dedicated
	// VRAM, so it is reported as what it is rather than as zero. Zero meant
	// the sizer declined to derive and the configured literal was used
	// unchallenged: on a 16 GB Mac that was 32768, which wired 12.54 GB and
	// left 60 MB free. CoreAudio could not allocate a stream buffer at that
	// point and every spoken reply was synthesised and silently dropped.
	// `Unified` is what tells the sizer this budget is the whole machine's.
	b, err = r.Run(ctx, "sysctl", "-n", "hw.memsize")
	total, ok := parseIntLoose(string(b))
	if err != nil || !ok || total < 1 {
		return Result{Metal, name, 0, false}, nil
	}
	return Result{Metal, name, total, true}, nil
}

// DeviceInspector asks llama itself, including dynamically loaded backends.
type DeviceInspector struct{ Runner Runner }

func (d DeviceInspector) Backend(ctx context.Context, path string) (Backend, error) {
	b, err := d.Runner.Run(ctx, path, "--list-devices")
	if err != nil {
		return CPU, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "MTL") && strings.Contains(line, ": Apple ") {
			return Backend(Metal), nil
		}
	}
	return CPU, nil
}
