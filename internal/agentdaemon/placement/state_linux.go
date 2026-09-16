//go:build linux

package placement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

func (c *Controller) recordPath(id string) string { return filepath.Join(c.root, id+".json") }

func privateFile(f *os.File) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || st.Uid != uint32(os.Getuid()) || st.Nlink != 1 {
		return errors.New("placement state must be a private, owned regular file")
	}
	return nil
}

func secureDirectory(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("state directory must be canonical and absolute")
	}
	if path != "/" {
		if err := secureDirectory(filepath.Dir(path)); err != nil {
			return err
		}
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		parent, syncErr := os.Open(filepath.Dir(path))
		if syncErr != nil {
			return syncErr
		}
		syncErr = parent.Sync()
		closeErr := parent.Close()
		if syncErr != nil {
			return syncErr
		}
		if closeErr != nil {
			return closeErr
		}
		info, err = os.Lstat(path)
	}
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode().Perm()&0022 != 0 || (st.Uid != 0 && st.Uid != uint32(os.Getuid())) {
		return fmt.Errorf("untrusted placement state directory: %s", path)
	}
	return nil
}

func (c *Controller) lock(ctx context.Context, id string) (func(), error) {
	if !fullID.MatchString(id) {
		return nil, errors.New("require full immutable container ID")
	}
	if err := secureDirectory(c.root); err != nil {
		return nil, err
	}
	info, err := os.Stat(c.root)
	if err != nil || info.Mode().Perm() != 0700 {
		return nil, errors.New("placement state directory must have mode 0700")
	}
	f, err := os.OpenFile(filepath.Join(c.root, id+".lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if err := privateFile(f); err != nil {
		f.Close()
		return nil, err
	}
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK {
			f.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
	return func() { _ = f.Close() }, nil
}

func (c *Controller) load(id string) (*Receipt, error) {
	f, err := os.OpenFile(c.recordPath(id), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err := privateFile(f); err != nil {
		return nil, err
	}
	var r Receipt
	if err := json.NewDecoder(f).Decode(&r); err != nil {
		return nil, err
	}
	if r.Version != 1 || r.Target.Container != id || !ownerID.MatchString(r.Owner) ||
		(r.State != "enrolled" && r.State != "stopping" && r.State != "retired") ||
		(r.State == "retired") != (r.RetiredAt != nil) {
		return nil, errors.New("invalid placement receipt")
	}
	return &r, nil
}

func (c *Controller) save(r *Receipt) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(c.root, ".receipt-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(data, '\n')); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(f.Name(), c.recordPath(r.Target.Container)); err != nil {
		return err
	}
	return c.syncDir(c.root)
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
