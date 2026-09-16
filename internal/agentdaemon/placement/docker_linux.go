//go:build linux

package placement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Do not inherit a remote Docker context while observing local /proc and cgroups.
func runDocker(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "docker", append([]string{"--host", "unix:///var/run/docker.sock"}, args...)...)
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "DOCKER_") {
			cmd.Env = append(cmd.Env, v)
		}
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("local Docker %s failed: %w", args[0], err)
	}
	return out, nil
}

type container struct {
	ID           string `json:"Id"`
	Created      string
	Image        string
	RestartCount int
	State        struct {
		Running    bool
		Pid        int
		StartedAt  string
		Paused     bool
		Restarting bool
	}
	Config struct {
		Labels map[string]string
		User   string
	}
	HostConfig struct {
		Privileged        bool
		PidMode           string
		IpcMode           string
		CgroupnsMode      string
		NetworkMode       string
		CapAdd            []string
		CapDrop           []string
		SecurityOpt       []string
		Devices           []json.RawMessage
		DeviceRequests    []json.RawMessage
		DeviceCgroupRules []string
		VolumesFrom       []string
		RestartPolicy     struct{ Name string }
	}
	Mounts []struct {
		Type, Source, Destination, Propagation string
		RW                                     bool
	}
}

func (c *Controller) inspect(ctx context.Context, id string) (*container, error) {
	out, err := c.run(ctx, "container", "inspect", id)
	if err != nil {
		return nil, err
	}
	var units []container
	if err := json.Unmarshal(out, &units); err != nil {
		return nil, err
	}
	if len(units) != 1 || units[0].ID != id {
		return nil, errors.New("supervisor returned wrong target")
	}
	return &units[0], nil
}

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

func inside(parent, path string) bool {
	return path == parent || strings.HasPrefix(path, parent+string(os.PathSeparator))
}

func (c *Controller) validateProfile(u *container, owner, workspace string) error {
	h := u.HostConfig
	uid, err := strconv.Atoi(strings.Split(u.Config.User, ":")[0])
	if err != nil || uid <= 0 || u.Config.Labels[BindingLabel] != owner || u.State.Paused || u.State.Restarting ||
		h.Privileged || h.PidMode != "" || h.IpcMode != "private" || h.CgroupnsMode != "private" ||
		h.NetworkMode != "none" || len(h.CapAdd) != 0 || !contains(h.CapDrop, "ALL") ||
		(len(h.SecurityOpt) != 1 || !contains(h.SecurityOpt, "no-new-privileges")) || len(h.Devices)+len(h.DeviceRequests)+len(h.DeviceCgroupRules)+len(h.VolumesFrom) != 0 ||
		h.RestartPolicy.Name != "no" {
		return errors.New("placement is outside qualified unprivileged local Docker profile")
	}
	if !filepath.IsAbs(workspace) || filepath.Clean(workspace) != workspace {
		return errors.New("workspace must be canonical and absolute")
	}
	resolved, err := filepath.EvalSymlinks(workspace)
	if err != nil || resolved != workspace {
		return errors.New("workspace must exist without symlink aliases")
	}
	info, err := os.Stat(workspace)
	if err != nil || !info.IsDir() {
		return errors.New("workspace must be a directory")
	}
	var fs syscall.Statfs_t
	if err := syscall.Statfs(workspace, &fs); err != nil {
		return err
	}
	switch uint64(fs.Type) {
	case 0xef53, 0x58465342, 0x9123683e, 0x01021994:
	default:
		return errors.New("unqualified workspace filesystem")
	}
	found := false
	for _, m := range u.Mounts {
		canonical, err := filepath.EvalSymlinks(m.Source)
		if err != nil || canonical != m.Source || inside(m.Source, c.root) || inside(c.root, m.Source) {
			return errors.New("mount aliases or exposes controller state")
		}
		if m.Type != "bind" || (m.Propagation != "rprivate" && m.Propagation != "") {
			return errors.New("only private bind mounts are qualified")
		}
		if m.Source == workspace && m.RW && !found {
			found = true
			continue
		}
		info, err := os.Stat(m.Source)
		if err != nil || m.RW || !info.Mode().IsRegular() {
			return errors.New("additional mounts must be read-only regular files")
		}
	}
	if !found {
		return errors.New("missing exact retained workspace bind")
	}
	// Nested host mounts could expose another filesystem or controller data.
	mounts, err := os.ReadFile(filepath.Join(c.procRoot, "self/mountinfo"))
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(mounts), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		point := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(fields[4])
		if point != workspace && inside(workspace, point) {
			return errors.New("nested workspace mounts are unqualified")
		}
	}
	return nil
}
