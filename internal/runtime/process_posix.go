//go:build linux || darwin

package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/JulianAbeleda/arkey_v3/internal/platform"
)

// The types in this file are POSIX-portable: DirectLauncher relies only on
// Setpgid/kill, FileLock on flock, and HTTPHealth on net/http. They are shared
// by the Linux (procfs) and macOS backends, which differ only in how they read
// process identity and manage the service.

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

// CommandRunner runs a program with an argv vector (never a shell string) and
// returns its combined output. It is injected so the service and library
// backends stay testable.
type CommandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
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
