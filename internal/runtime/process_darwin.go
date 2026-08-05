//go:build darwin

package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/JulianAbeleda/arkey_v3/internal/platform"
)

// macOS has no procfs, so the darwin backend reads process identity through the
// base-system ps(1) and lsof(8) utilities via an injected CommandRunner (never a
// shell). The persisted identity triple is self-consistent — Save and the later
// ownership check derive it the same way — which is all the controller requires;
// it need not match the Linux representation.

// lstartLayout is ctime(3) as emitted by `ps -o lstart` (day space-padded to
// two columns), e.g. "Wed Aug  5 12:16:48 2026".
const lstartLayout = "Mon Jan _2 15:04:05 2006"

// DarwinInspector reads executable path, argument fingerprint, and start time
// from ps(1), and resolves listening-port ownership from lsof(8).
type DarwinInspector struct{ Runner CommandRunner }

func (i DarwinInspector) runner() CommandRunner {
	if i.Runner != nil {
		return i.Runner
	}
	return platform.ExecRunner{}
}

func (i DarwinInspector) psField(ctx context.Context, pid int, field string) (string, error) {
	if pid < 1 {
		return "", errors.New("runtime: invalid process id")
	}
	out, err := i.runner().Run(ctx, "ps", "-ww", "-o", field+"=", "-p", strconv.Itoa(pid))
	if err != nil {
		return "", fmt.Errorf("runtime: ps %s for pid %d: %w", field, pid, err)
	}
	value := strings.TrimSpace(string(out))
	if value == "" {
		return "", fmt.Errorf("runtime: no such process %d", pid)
	}
	return value, nil
}

func (i DarwinInspector) Process(ctx context.Context, pid int) (Process, error) {
	exe, err := i.psField(ctx, pid, "comm")
	if err != nil {
		return Process{}, err
	}
	command, err := i.psField(ctx, pid, "command")
	if err != nil {
		return Process{}, err
	}
	lstart, err := i.psField(ctx, pid, "lstart")
	if err != nil {
		return Process{}, err
	}
	started, err := time.Parse(lstartLayout, lstart)
	if err != nil {
		return Process{}, fmt.Errorf("runtime: parse process start time %q: %w", lstart, err)
	}
	sum := sha256.Sum256([]byte(command))
	return Process{
		PID:             pid,
		Executable:      exe,
		ArgsFingerprint: hex.EncodeToString(sum[:]),
		StartTime:       uint64(started.Unix()),
	}, nil
}

// PortOwner returns the pid of the process listening on port, or 0 when none is
// found. lsof exits non-zero with empty output when nothing matches, which is
// reported as "no owner" (0, nil) to match the Linux backend's contract.
func (i DarwinInspector) PortOwner(ctx context.Context, port int) (int, error) {
	if port < 1 || port > 65535 {
		return 0, errors.New("runtime: invalid port")
	}
	out, _ := i.runner().Run(ctx, "lsof", "-nP", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-t")
	for _, line := range strings.Fields(string(out)) {
		if pid, err := strconv.Atoi(line); err == nil && pid > 0 {
			return pid, nil
		}
	}
	return 0, nil
}

// OtoolBackend inspects GPU alignment on macOS. It mirrors the Linux backend but
// reads shared-library linkage through otool(1) rather than ldd(1). In practice
// the local-serving path requires an nvidia/amd vendor, so on Apple Silicon this
// is reached only on the (rare) discrete-GPU configurations that pass GPU scan.
type OtoolBackend struct{ Runner CommandRunner }

func (b OtoolBackend) runner() CommandRunner {
	if b.Runner != nil {
		return b.Runner
	}
	return platform.ExecRunner{}
}

func (b OtoolBackend) Aligned(ctx context.Context, exe, vendor string) (bool, error) {
	out, e := b.runner().Run(ctx, "otool", "-L", exe)
	if e != nil {
		return false, e
	}
	s := string(out)
	switch vendor {
	case "nvidia":
		return strings.Contains(s, "cuda") || strings.Contains(s, "CUDA"), nil
	case "amd":
		return strings.Contains(s, "rocm") || strings.Contains(s, "ROCm") || strings.Contains(s, "hip"), nil
	}
	return false, nil
}

func (OtoolBackend) Accelerated(_ context.Context, log, vendor string) (bool, error) {
	b, e := os.ReadFile(log)
	if e != nil {
		return false, e
	}
	s := string(b)
	if vendor == "nvidia" {
		return strings.Contains(s, "CUDA"), nil
	}
	return strings.Contains(s, "ROCm") || strings.Contains(s, "HIP"), nil
}

// DirectOnlyService reports no managed-service backend on macOS. macOS ships
// launchd rather than systemd, and Arkey does not install a launchd job for the
// transient llama/MoonBridge processes; reporting Available()==false keeps the
// controller and bridge on the fully supported direct-process path, where
// startup uses DirectLauncher and teardown uses its process-group Terminate.
type DirectOnlyService struct{}

func (DirectOnlyService) Available(context.Context) bool { return false }
func (DirectOnlyService) Start(context.Context, string, []string, string) (int, error) {
	return 0, errors.New("runtime: managed service start is unavailable on macOS; use direct launch")
}
func (DirectOnlyService) Stop(context.Context, string) error { return nil }

// Alive satisfies ProcessLiveness. Not derived from Process(), which shells out
// to ps and cannot distinguish "no such pid" from any other ps failure.
func (DarwinInspector) Alive(pid int) bool { return ProcessAlive(pid) }
