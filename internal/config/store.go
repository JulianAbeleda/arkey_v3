package config

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/JulianAbeleda/arkey_v3/internal/platform"
)

type Store struct{ Path, Home string }

func (s Store) Load() (Config, error) {
	if e := platform.RejectSymlinkComponents(s.Path); e != nil {
		return Config{}, e
	}
	fi, e := os.Lstat(s.Path)
	if e != nil {
		return Config{}, e
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return Config{}, errors.New("refusing symlinked config")
	}
	if fi.Size() > 1<<20 {
		return Config{}, errors.New("config exceeds 1 MiB safety limit")
	}
	b, e := os.ReadFile(s.Path)
	if e != nil {
		return Config{}, e
	}
	return Decode(b, s.Home)
}
func (s Store) Save(c Config) error {
	if e := platform.RejectSymlinkComponents(s.Path); e != nil {
		return e
	}
	if _, e := os.Lstat(s.Path); e == nil {
		fi, _ := os.Lstat(s.Path)
		if fi.Mode()&os.ModeSymlink != 0 {
			return errors.New("refusing symlinked config")
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	b, e := Encode(c)
	if e != nil {
		return e
	}
	if e = platform.EnsurePrivateDir(filepath.Dir(s.Path)); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(s.Path), ".config.*")
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
	if ce := f.Close(); e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	if err := os.Rename(n, s.Path); err != nil {
		return err
	}
	if dir, err := os.Open(filepath.Dir(s.Path)); err == nil {
		defer dir.Close()
		_ = dir.Sync()
	}
	return nil
}
