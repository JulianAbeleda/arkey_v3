//go:build linux

package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/JulianAbeleda/arkey_v3/internal/platform"
)

// LinuxInspector reads process identity from procfs. All three persisted fields
// must agree before a process can be stopped.
type LinuxInspector struct{}

func (LinuxInspector) Process(_ context.Context, pid int) (Process, error) {
	exe, e := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if e != nil {
		return Process{}, e
	}
	cmd, e := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if e != nil {
		return Process{}, e
	}
	stat, e := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if e != nil {
		return Process{}, e
	}
	fields := strings.Fields(string(stat))
	if len(fields) < 22 {
		return Process{}, errors.New("runtime: malformed proc stat")
	}
	started, e := strconv.ParseUint(fields[21], 10, 64)
	if e != nil {
		return Process{}, e
	}
	sum := sha256.Sum256(cmd)
	return Process{PID: pid, Executable: exe, ArgsFingerprint: hex.EncodeToString(sum[:]), StartTime: started}, nil
}

// PortOwner finds an IPv4/IPv6 listening socket and maps its inode back to a
// process FD. It avoids invoking a shell or relying on optional utilities.
func (LinuxInspector) PortOwner(_ context.Context, port int) (int, error) {
	inodes := map[string]bool{}
	for _, name := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		b, err := os.ReadFile(name)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n")[1:] {
			f := strings.Fields(line)
			if len(f) < 10 || f[3] != "0A" {
				continue
			}
			pair := strings.Split(f[1], ":")
			if len(pair) != 2 {
				continue
			}
			p, err := strconv.ParseInt(pair[1], 16, 32)
			if err == nil && int(p) == port {
				inodes[f[9]] = true
			}
		}
	}
	if len(inodes) == 0 {
		return 0, nil
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, err
	}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		fds, err := os.ReadDir(filepath.Join("/proc", entry.Name(), "fd"))
		if err != nil {
			continue
		}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join("/proc", entry.Name(), "fd", fd.Name()))
			if err == nil && inodes[strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]")] {
				return pid, nil
			}
		}
	}
	return 0, nil
}

type DirectLauncher struct{}

func (DirectLauncher) StartDirect(ctx context.Context, args []string, logPath string) (int, error) {
	if len(args) == 0 {
		return 0, errors.New("runtime: empty command")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if e := platform.EnsurePrivateDir(filepath.Dir(logPath)); e != nil {
		return 0, e
	}
	f, e := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e != nil {
		return 0, e
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdout = f
	cmd.Stderr = f
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if e = cmd.Start(); e != nil {
		_ = f.Close()
		return 0, e
	}
	go func() { _ = cmd.Wait() }()
	_ = f.Close()
	return cmd.Process.Pid, nil
}
func (DirectLauncher) Terminate(ctx context.Context, pid int) error {
	if pid < 1 {
		return errors.New("runtime: invalid process id")
	}
	signal := func(sig syscall.Signal) error {
		err := syscall.Kill(-pid, sig)
		if errors.Is(err, syscall.ESRCH) {
			err = syscall.Kill(pid, sig)
		}
		return err
	}
	alive := func() bool {
		if err := syscall.Kill(-pid, 0); err == nil || errors.Is(err, syscall.EPERM) {
			return true
		}
		err := syscall.Kill(pid, 0)
		return err == nil || errors.Is(err, syscall.EPERM)
	}
	if err := signal(syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if !alive() {
			return nil
		}
		select {
		case <-ctx.Done():
			_ = signal(syscall.SIGKILL)
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

type HTTPHealth struct{ Client *http.Client }

func (h HTTPHealth) LlamaHealthy(ctx context.Context, port int) (bool, error) {
	cl := h.Client
	if cl == nil {
		cl = &http.Client{Timeout: time.Second}
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/v1/models", port), nil)
	if e != nil {
		return false, e
	}
	r, e := cl.Do(req)
	if e != nil {
		return false, e
	}
	defer r.Body.Close()
	return r.StatusCode >= 200 && r.StatusCode < 300, nil
}

type CommandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}
type CommandBackend struct{ Runner CommandRunner }

func (b CommandBackend) Aligned(ctx context.Context, exe, vendor string) (bool, error) {
	out, e := b.Runner.Run(ctx, "ldd", exe)
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
func (CommandBackend) Accelerated(_ context.Context, log, vendor string) (bool, error) {
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

// FileLock is advisory and held for the duration of an operation.
type FileLock struct{ Path string }

func (l FileLock) Lock(ctx context.Context) (func() error, error) {
	if e := platform.RejectSymlinkComponents(l.Path); e != nil {
		return nil, e
	}
	if e := platform.EnsurePrivateDir(filepath.Dir(l.Path)); e != nil {
		return nil, e
	}
	f, e := os.OpenFile(l.Path, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	for {
		e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if e == nil {
			return func() error {
				e1 := syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				e2 := f.Close()
				if e1 != nil {
					return e1
				}
				return e2
			}, nil
		}
		if e != syscall.EWOULDBLOCK && e != syscall.EAGAIN {
			_ = f.Close()
			return nil, e
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// SystemdService uses a transient user unit when systemd --user is reachable.
// It intentionally passes an argv vector to systemd-run, not a shell command.
type SystemdService struct{ Runner CommandRunner }

func (s SystemdService) Available(ctx context.Context) bool {
	_, e := s.Runner.Run(ctx, "systemctl", "--user", "show-environment")
	return e == nil
}
func (s SystemdService) Start(ctx context.Context, unit string, args []string, log string) (int, error) {
	a := []string{"--user", "--quiet", "--collect", "--unit=" + unit, "--property=Restart=on-failure", "--property=StandardOutput=append:" + log, "--property=StandardError=append:" + log}
	a = append(a, args...)
	if _, e := s.Runner.Run(ctx, "systemd-run", a...); e != nil {
		return 0, e
	}
	for i := 0; i < 80; i++ {
		out, e := s.Runner.Run(ctx, "systemctl", "--user", "show", unit+".service", "--property=MainPID", "--value")
		if e == nil {
			if p, _ := strconv.Atoi(strings.TrimSpace(string(out))); p > 0 {
				return p, nil
			}
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return 0, errors.New("runtime: systemd unit did not produce a pid")
}
func (s SystemdService) Stop(ctx context.Context, unit string) error {
	_, e := s.Runner.Run(ctx, "systemctl", "--user", "stop", unit+".service")
	return e
}
