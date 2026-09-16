//go:build linux

package placement

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func (c *Controller) process(pid int) (Process, bool, error) {
	if pid <= 0 {
		return Process{}, false, errors.New("missing process identity")
	}
	data, err := os.ReadFile(filepath.Join(c.procRoot, strconv.Itoa(pid), "stat"))
	if err != nil {
		return Process{}, false, err
	}
	end := strings.LastIndexByte(string(data), ')')
	if end < 0 {
		return Process{}, false, errors.New("malformed process identity")
	}
	fields := strings.Fields(string(data[end+1:]))
	if len(fields) < 20 {
		return Process{}, false, errors.New("incomplete process identity")
	}
	return Process{PID: pid, Start: fields[19]}, fields[0] != "Z" && fields[0] != "X", nil
}

func (c *Controller) group(pid int, id string) (string, error) {
	data, err := os.ReadFile(filepath.Join(c.procRoot, strconv.Itoa(pid), "cgroup"))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "0::/") {
			continue
		}
		path := strings.TrimPrefix(line, "0::")
		base := filepath.Base(path)
		if filepath.Clean(path) != path || (base != id && base != "docker-"+id+".scope") {
			return "", errors.New("cgroup does not identify the exact container")
		}
		return path, nil
	}
	return "", errors.New("placement requires cgroup v2")
}

func (c *Controller) groupPath(group string) (string, error) {
	if !filepath.IsAbs(group) || filepath.Clean(group) != group || group == "/" {
		return "", errors.New("invalid saved cgroup")
	}
	return filepath.Join(c.cgroupRoot, group), nil
}

func (c *Controller) members(group string) ([]Process, error) {
	path, err := c.groupPath(group)
	if err != nil {
		return nil, err
	}
	var members []Process
	err = filepath.WalkDir(path, func(p string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Name() != "cgroup.procs" {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, value := range strings.Fields(string(data)) {
			pid, err := strconv.Atoi(value)
			if err != nil {
				return err
			}
			identity, _, err := c.process(pid)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return err
			}
			members = append(members, identity)
		}
		return nil
	})
	return members, err
}

func (c *Controller) observeRetired(r *Receipt) error {
	path, err := c.groupPath(r.Target.Cgroup)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(path, "cgroup.events"))
	if errors.Is(err, os.ErrNotExist) {
		if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
			return errors.New("cgroup evidence unavailable")
		}
	} else if err != nil {
		return err
	} else if !strings.Contains("\n"+string(data), "\npopulated 0\n") {
		return errors.New("placement cgroup remains populated")
	}
	for _, old := range append(append([]Process{}, r.Members...), r.Target.Init) {
		current, live, err := c.process(old.PID)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if current == old && live {
			return fmt.Errorf("old placement process %d remains live", old.PID)
		}
	}
	return nil
}
