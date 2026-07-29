package models

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Model struct {
	Path, Name, Parent string
	Size               int64
	Selected, Running  bool
}
type Discovery struct {
	Models  []Model
	Ignored int
}

func Discover(ctx context.Context, roots []string) (Discovery, error) {
	var out Discovery
	seen := map[string]bool{}
	for _, root := range roots {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		root, err := filepath.Abs(root)
		if err != nil {
			return out, err
		}
		if seen[root] {
			continue
		}
		seen[root] = true
		err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				out.Ignored++
				return nil
			}
			if e := ctx.Err(); e != nil {
				return e
			}
			if d.Type()&os.ModeSymlink != 0 {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() || !strings.EqualFold(filepath.Ext(d.Name()), ".gguf") {
				return nil
			}
			info, e := d.Info()
			if e != nil || !info.Mode().IsRegular() {
				out.Ignored++
				return nil
			}
			out.Models = append(out.Models, Model{Path: path, Name: strings.TrimSuffix(d.Name(), filepath.Ext(d.Name())), Parent: filepath.Dir(path), Size: info.Size()})
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return out, err
		}
	}
	sort.Slice(out.Models, func(i, j int) bool {
		a, b := out.Models[i], out.Models[j]
		if a.Name == b.Name {
			return a.Path < b.Path
		}
		return a.Name < b.Name
	})
	return out, nil
}
