package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/JulianAbeleda/arkey_v3/internal/platform"
)

// FileStore writes a private runtime.json. It never follows a symlink target.
type FileStore struct{ Path string }

func (s FileStore) Load(context.Context) (State, error) {
	if err := platform.RejectSymlinkComponents(s.Path); err != nil {
		return State{}, err
	}
	if info, err := os.Lstat(s.Path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return State{}, errors.New("runtime: state path is a symlink")
		}
		if info.Size() > 1<<20 {
			return State{}, errors.New("runtime: state exceeds 1 MiB safety limit")
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return State{}, err
	}
	b, e := os.ReadFile(s.Path)
	if errors.Is(e, os.ErrNotExist) {
		return State{}, ErrNoState
	}
	if e != nil {
		return State{}, e
	}
	var v State
	if e = json.Unmarshal(b, &v); e != nil {
		return State{}, e
	}
	return v, nil
}
func (s FileStore) Save(_ context.Context, v State) error {
	if err := platform.RejectSymlinkComponents(s.Path); err != nil {
		return err
	}
	if i, e := os.Lstat(s.Path); e == nil && i.Mode()&os.ModeSymlink != 0 {
		return errors.New("runtime: state path is a symlink")
	}
	if e := platform.EnsurePrivateDir(filepath.Dir(s.Path)); e != nil {
		return e
	}
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(s.Path), ".runtime-*")
	if e != nil {
		return e
	}
	n := f.Name()
	defer os.Remove(n)
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
	if e == nil {
		e = f.Sync()
	}
	if e2 := f.Close(); e == nil {
		e = e2
	}
	if e != nil {
		return e
	}
	return os.Rename(n, s.Path)
}
func (s FileStore) Clear(context.Context) error {
	if err := platform.RejectSymlinkComponents(s.Path); err != nil {
		return err
	}
	e := os.Remove(s.Path)
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	return e
}
