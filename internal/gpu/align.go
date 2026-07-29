package gpu

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Backend string

const (
	CPU         Backend = "cpu"
	CUDABackend Backend = "nvidia"
	ROCmBackend Backend = "amd"
)

type Inspector interface {
	Backend(context.Context, string) (Backend, error)
}
type LDDInspector struct{ Runner Runner }

func (d LDDInspector) Backend(ctx context.Context, path string) (Backend, error) {
	b, e := d.Runner.Run(ctx, "ldd", path)
	if e != nil {
		return CPU, e
	}
	s := strings.ToLower(string(b))
	if strings.Contains(s, "libggml-cuda") || strings.Contains(s, "libcuda") || strings.Contains(s, "libcudart") || strings.Contains(s, "libcublas") {
		return CUDABackend, nil
	}
	if strings.Contains(s, "libggml-hip") || strings.Contains(s, "libamdhip") || strings.Contains(s, "libhipblas") || strings.Contains(s, "librocblas") {
		return ROCmBackend, nil
	}
	return CPU, nil
}
func CandidateServers(ctx context.Context, roots []string) ([]string, error) {
	var out []string
	for _, r := range roots {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		e := filepath.WalkDir(r, func(p string, d os.DirEntry, e error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if e != nil {
				return nil
			}
			if d.Type()&os.ModeSymlink != 0 {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.IsDir() && d.Name() == "llama-server" && d.Type().IsRegular() {
				if i, x := d.Info(); x == nil && i.Mode().Perm()&0111 != 0 {
					out = append(out, p)
				}
			}
			return nil
		})
		if e != nil && !os.IsNotExist(e) {
			return nil, e
		}
	}
	sort.Strings(out)
	return out, nil
}
func FindAligned(ctx context.Context, v Vendor, candidates []string, inspect Inspector) (string, error) {
	want := Backend(v)
	for _, p := range candidates {
		b, e := inspect.Backend(ctx, p)
		if e == nil && b == want {
			return p, nil
		}
	}
	return "", os.ErrNotExist
}
