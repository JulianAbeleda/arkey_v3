package models

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"encoding/json"
	"errors"
	"fmt"
	"io"
)

func DiscoverFor(ctx context.Context, roots []string, engine string) (Discovery, error) {
	if engine != "vllm" {
		return Discover(ctx, roots)
	}
	var out Discovery
	seen := map[string]bool{}
	for _, root := range roots {
		root, err := filepath.EvalSymlinks(root)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return out, err
		}
		err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				out.Ignored++
				return nil
			}
			if !d.IsDir() {
				return nil
			}
			if _, err := HFModelType(path); err != nil {
				return nil
			}
			canonical, err := filepath.Abs(path)
			if err != nil {
				return err
			}
			if !seen[canonical] {
				var size int64
				shards, _ := filepath.Glob(filepath.Join(path, "*.safetensors"))
				for _, p := range shards {
					if info, e := os.Stat(p); e == nil {
						size += info.Size()
					}
				}
				out.Models = append(out.Models, Model{Path: canonical, Name: filepath.Base(path), Parent: filepath.Dir(path), Size: size})
				seen[canonical] = true
			}
			return filepath.SkipDir
		})
		if err != nil {
			return out, err
		}
	}
	sort.Slice(out.Models, func(i, j int) bool {
		if out.Models[i].Name == out.Models[j].Name {
			return out.Models[i].Path < out.Models[j].Path
		}
		return out.Models[i].Name < out.Models[j].Name
	})
	return out, nil
}

func HFModelType(path string) (string, error) {
	f, err := os.Open(filepath.Join(path, "config.json"))
	if err != nil {
		return "", fmt.Errorf("runtime: checkpoint config: %w", err)
	}
	defer f.Close()
	var cfg struct {
		ModelType string `json:"model_type"`
	}
	if err = json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&cfg); err != nil || cfg.ModelType == "" {
		return "", errors.New("runtime: checkpoint requires a valid config.json with model_type")
	}
	files, err := filepath.Glob(filepath.Join(path, "*.safetensors"))
	if err != nil || len(files) == 0 {
		return "", errors.New("runtime: checkpoint has no safetensors weights")
	}
	return cfg.ModelType, nil
}
