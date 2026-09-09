package gpu

import (
	"context"
	"strings"
)

func detectPlatform(ctx context.Context, r Runner) (Result, error) {
	b, err := r.Run(ctx, "sysctl", "-n", "machdep.cpu.brand_string")
	name := strings.TrimSpace(string(b))
	if err != nil || !strings.HasPrefix(name, "Apple ") {
		return Result{Unknown, "No supported GPU", 0}, nil
	}
	// Unified memory is shared with the OS and applications, not dedicated VRAM.
	return Result{Metal, name, 0}, nil
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
