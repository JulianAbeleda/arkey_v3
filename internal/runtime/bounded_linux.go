//go:build linux

package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// BoundedService keeps heavy model processes outside the user-session cgroup.
// No unbounded fallback and no automatic restart loop after OOM/startup failure.
type BoundedService struct {
	Runner      CommandRunner
	MemoryBytes uint64
}

func (s BoundedService) Available(ctx context.Context) bool {
	_, err := s.Runner.Run(ctx, "sudo", "-n", "systemctl", "show", "--property=Version")
	return err == nil
}
func (s BoundedService) Start(ctx context.Context, unit string, args []string, log string) (int, error) {
	return s.StartConfigured(ctx, unit, args, log, nil)
}
func (s BoundedService) StartConfigured(ctx context.Context, unit string, args []string, log string, env map[string]string) (int, error) {
	if s.MemoryBytes == 0 {
		return 0, errors.New("runtime: vLLM memory cap must be configured")
	}
	limit, err := MemoryCap(s.MemoryBytes)
	if err != nil {
		return 0, err
	}
	a := []string{"-n", "systemd-run", "--quiet", "--collect", "--unit=" + unit, "--uid=" + strconv.Itoa(os.Getuid()), "--gid=" + strconv.Itoa(os.Getgid()), "--property=Slice=arkey-inference.slice", "--property=MemoryMax=" + strconv.FormatUint(limit, 10), "--property=MemorySwapMax=0", "--property=OOMPolicy=stop", "--property=KillMode=control-group", "--property=Restart=no", "--property=TimeoutStopSec=20", "--property=StandardOutput=append:" + log, "--property=StandardError=append:" + log}
	// system services do not inherit the terminal's environment. Keep startup
	// reproducible and let the configured environment override these defaults.
	environment := map[string]string{"HOME": os.Getenv("HOME"), "PATH": os.Getenv("PATH"), "MAX_JOBS": "1", "NINJA_NUM_JOBS": "1"}
	for k, v := range env {
		environment[k] = v
	}
	keys := make([]string, 0, len(environment))
	for k := range environment {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		a = append(a, "--setenv="+k+"="+environment[k])
	}
	a = append(a, "--")
	a = append(a, args...)
	if _, err := s.Runner.Run(ctx, "sudo", a...); err != nil {
		return 0, err
	}
	for i := 0; i < 80; i++ {
		if pid, err := s.MainPID(ctx, unit); err == nil && pid > 0 {
			return pid, nil
		}
		select {
		case <-ctx.Done():
			_ = s.stopAfterStart(unit)
			return 0, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	_ = s.stopAfterStart(unit)
	return 0, errors.New("runtime: bounded service did not produce a PID")
}
func (s BoundedService) stopAfterStart(unit string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.Stop(ctx, unit)
}
func (s BoundedService) MainPID(ctx context.Context, unit string) (int, error) {
	out, err := s.Runner.Run(ctx, "sudo", "-n", "systemctl", "show", unit+".service", "--property=MainPID", "--value")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}
func (s BoundedService) Stop(ctx context.Context, unit string) error {
	_, err := s.Runner.Run(ctx, "sudo", "-n", "systemctl", "stop", unit+".service")
	return err
}

// MemoryCap reserves at least 25% of physical memory for the desktop and other
// processes, even if an operator accidentally configures a larger cap.
func MemoryCap(requested uint64) (uint64, error) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	var total, available uint64
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			_, err = fmt.Sscanf(line, "MemTotal: %d kB", &total)
		}
		if strings.HasPrefix(line, "MemAvailable:") {
			_, err = fmt.Sscanf(line, "MemAvailable: %d kB", &available)
		}
	}
	if err != nil || total == 0 {
		return 0, errors.New("runtime: cannot determine host memory")
	}
	ceiling := total * 1024 * 3 / 4
	if available*1024 <= 2<<30 {
		return 0, errors.New("runtime: insufficient available host memory")
	}
	if headroom := available*1024 - (2 << 30); headroom < ceiling {
		ceiling = headroom
	}
	if requested == 0 || requested > ceiling {
		return ceiling, nil
	}
	return requested, nil
}
