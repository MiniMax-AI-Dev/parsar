//go:build linux

package placement

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type hostMount struct {
	device, root, point string
}

// The initial profile excludes host mount aliases rather than trying to resolve
// arbitrary backing-path graphs. Mount administration remains a trusted host act.
func (c *Controller) hostMounts(workspace string) ([]hostMount, error) {
	data, err := os.ReadFile(filepath.Join(c.procRoot, "self/mountinfo"))
	if err != nil {
		return nil, err
	}
	decode := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	var mounts []hostMount
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 10 {
			return nil, errors.New("incomplete host mount evidence")
		}
		mount := hostMount{device: fields[2], root: decode.Replace(fields[3]), point: decode.Replace(fields[4])}
		if !filepath.IsAbs(mount.root) || !filepath.IsAbs(mount.point) || filepath.Clean(mount.point) != mount.point {
			return nil, errors.New("invalid host mount evidence")
		}
		if mount.point != workspace && inside(workspace, mount.point) {
			return nil, errors.New("nested workspace mounts are unqualified")
		}
		mounts = append(mounts, mount)
	}
	return mounts, nil
}

func unaliasedSource(source string, mounts []hostMount) error {
	selected := -1
	for i, mount := range mounts {
		if !inside(mount.point, source) {
			continue
		}
		if selected == -1 || len(mount.point) > len(mounts[selected].point) {
			selected = i
		}
	}
	if selected == -1 || mounts[selected].root != "/" {
		return errors.New("mount source lacks whole-filesystem evidence; host aliases and subvolume roots are unqualified")
	}
	count := 0
	for _, mount := range mounts {
		if mount.device == mounts[selected].device {
			count++
		}
		// A stacked mount point is ambiguous even if it uses another device.
		if mount.point == mounts[selected].point && mount.device != mounts[selected].device {
			return errors.New("stacked source mounts are unqualified")
		}
	}
	if count != 1 {
		return errors.New("multiple host mounts of the source filesystem are unqualified")
	}
	return nil
}
