//go:build linux

package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
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

// LlamaProcess recognizes only Arkey's loopback llama command shape and
// returns the model from the live argv. It is used to survive systemd PID
// changes without weakening process-ownership checks.
func (i LinuxInspector) LlamaProcess(ctx context.Context, pid int, server string, port int) (Process, string, error) {
	process, err := i.Process(ctx, pid)
	if err != nil {
		return Process{}, "", err
	}
	canonicalServer, err := filepath.EvalSymlinks(server)
	if err != nil || process.Executable != canonicalServer {
		return Process{}, "", errors.New("runtime: llama executable mismatch")
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return Process{}, "", err
	}
	args := strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00")
	wants := map[string]string{"--alias": "arkey-local", "--host": "127.0.0.1", "--port": strconv.Itoa(port)}
	model := ""
	seen := map[string]bool{}
	for index := 1; index+1 < len(args); index++ {
		key, value := args[index], args[index+1]
		if key == "--model" {
			if seen[key] {
				return Process{}, "", errors.New("runtime: duplicate llama model argument")
			}
			seen[key], model = true, value
			index++
			continue
		}
		if expected, ok := wants[key]; ok {
			if seen[key] || value != expected {
				return Process{}, "", errors.New("runtime: llama arguments do not match Arkey")
			}
			seen[key] = true
			index++
		}
	}
	if model == "" || len(seen) != len(wants)+1 || !strings.EqualFold(filepath.Ext(model), ".gguf") {
		return Process{}, "", errors.New("runtime: incomplete Arkey llama arguments")
	}
	canonicalModel, err := filepath.EvalSymlinks(model)
	if err != nil {
		return Process{}, "", err
	}
	info, err := os.Stat(canonicalModel)
	if err != nil || !info.Mode().IsRegular() {
		return Process{}, "", errors.New("runtime: llama model is not a regular file")
	}
	return process, canonicalModel, nil
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

// CommandBackend inspects GPU alignment on Linux via ldd (shared-library
// linkage) and the llama-server log.
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
		if pid, e := s.MainPID(ctx, unit); e == nil && pid > 0 {
			return pid, nil
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return 0, errors.New("runtime: systemd unit did not produce a pid")
}
func (s SystemdService) MainPID(ctx context.Context, unit string) (int, error) {
	out, err := s.Runner.Run(ctx, "systemctl", "--user", "show", unit+".service", "--property=MainPID", "--value")
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, err
	}
	return pid, nil
}
func (s SystemdService) Stop(ctx context.Context, unit string) error {
	_, e := s.Runner.Run(ctx, "systemctl", "--user", "stop", unit+".service")
	return e
}

// Alive satisfies ProcessLiveness. Deliberately not derived from Process():
// that reads /proc/<pid>/exe, which fails with EACCES for a live process owned
// by another user -- indistinguishable, at the call site, from the ENOENT of a
// process that is gone. Signal 0 answers the liveness question directly.
func (LinuxInspector) Alive(pid int) bool { return ProcessAlive(pid) }
