//go:build linux

package placement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

var fullID = regexp.MustCompile(`^[a-f0-9]{64}$`)
var ownerID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

// Controller is the bounded local Linux/Docker retirement consumer. Its authority
// is the operator's private enrollment, never a public executor credential.
type Controller struct {
	root       string
	run        func(context.Context, ...string) ([]byte, error)
	procRoot   string
	cgroupRoot string
	socketPath string
	syncDir    func(string) error
}

// New uses a fixed local Docker endpoint and private state beneath ~/.parsar.
func New() (*Controller, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return &Controller{root: filepath.Join(home, ".parsar", "placements"), run: runDocker,
		procRoot: "/proc", cgroupRoot: "/sys/fs/cgroup", socketPath: localDockerSocket, syncDir: syncDirectory}, nil
}

func (c *Controller) enroll(ctx context.Context, id, owner, workspace, environment string) (*Receipt, error) {
	if !ownerID.MatchString(owner) {
		return nil, errors.New("invalid placement owner")
	}
	unlock, err := c.lock(ctx, id)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if _, err := os.Lstat(c.recordPath(id)); !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("placement already enrolled or record unavailable; use retire to reconcile")
	}
	unit, err := c.inspect(ctx, id)
	if err != nil {
		return nil, err
	}
	if !unit.State.Running {
		return nil, errors.New("enrollment requires a running placement")
	}
	if err := c.validateProfile(unit, owner, workspace); err != nil {
		return nil, err
	}
	host, supervisor, err := c.host(ctx)
	if err != nil {
		return nil, err
	}
	init, _, err := c.process(unit.State.Pid)
	if err != nil {
		return nil, err
	}
	group, err := c.group(init.PID, id)
	if err != nil {
		return nil, err
	}
	members, err := c.members(group)
	if err != nil {
		return nil, err
	}
	if len(members) == 0 {
		return nil, errors.New("running placement has no observed members")
	}
	version := 1
	if environment != "" {
		version = 2
	}
	r := &Receipt{Version: version, EnvironmentID: environment, State: "enrolled", Owner: owner, RequestedAt: time.Now().UTC(),
		Members: members, Target: Target{HostBootID: host, Supervisor: supervisor, Container: id,
			Created: unit.Created, Started: unit.State.StartedAt, Restarts: unit.RestartCount,
			Image: unit.Image, Workspace: workspace, Cgroup: group, Init: init}}
	if err := c.save(r); err != nil {
		return nil, err
	}
	return r, nil
}

func (c *Controller) retirePlacement(ctx context.Context, id, environment string) (*Receipt, error) {
	unlock, err := c.lock(ctx, id)
	if err != nil {
		return nil, err
	}
	defer unlock()
	r, err := c.load(id)
	if err != nil {
		return nil, err
	}
	if r.EnvironmentID != environment {
		return nil, errors.New("placement Environment does not match enrollment")
	}
	if r.State == "retired" {
		// A previous process may have published the rename without completing
		// its directory sync. Finish that barrier before recovering success.
		if err := c.syncDir(c.root); err != nil {
			return nil, fmt.Errorf("placement receipt durability unknown: %w", err)
		}
		return r, nil
	}
	err = c.retire(ctx, r)
	if err != nil {
		return r, fmt.Errorf("placement retirement unknown: %w", err)
	}
	return r, nil
}

func (c *Controller) retire(ctx context.Context, r *Receipt) error {
	host, supervisor, err := c.host(ctx)
	if err != nil {
		return err
	}
	if host != r.Target.HostBootID || supervisor != r.Target.Supervisor {
		return errors.New("local supervisor incarnation changed")
	}
	unit, err := c.inspect(ctx, r.Target.Container)
	if err != nil {
		return err
	}
	if err := c.validateProfile(unit, r.Owner, r.Target.Workspace); err != nil {
		return err
	}
	if unit.Created != r.Target.Created || unit.Image != r.Target.Image ||
		unit.State.StartedAt != r.Target.Started || unit.RestartCount != r.Target.Restarts {
		return errors.New("container incarnation changed")
	}
	if unit.State.Running {
		init, _, err := c.process(unit.State.Pid)
		if err != nil {
			return err
		}
		if init != r.Target.Init {
			return errors.New("container init identity changed")
		}
		group, err := c.group(init.PID, unit.ID)
		if err != nil || group != r.Target.Cgroup {
			return errors.New("container cgroup changed")
		}
		members, err := c.members(group)
		if err != nil {
			return err
		}
		r.Members = append(r.Members, members...)
	}
	// The immutable target and current members are durable before any stop.
	r.State = "stopping"
	if err := c.save(r); err != nil {
		return err
	}
	if unit.State.Running {
		if _, err := c.run(ctx, "container", "stop", "--time", "1", r.Target.Container); err != nil {
			return err
		}
	}
	unit, err = c.inspect(ctx, r.Target.Container)
	if err != nil {
		return err
	}
	if unit.State.Running || unit.State.Pid != 0 || unit.State.StartedAt != r.Target.Started ||
		unit.RestartCount != r.Target.Restarts {
		return errors.New("container is live or restarted")
	}
	if err := c.observeRetired(r); err != nil {
		return err
	}
	// Non-forced removal fails if an external operator restarted the unit. No
	// volumes are deleted. A crash between removal and save stays unknown.
	if _, err := c.run(ctx, "container", "rm", r.Target.Container); err != nil {
		return err
	}
	r.State = "retired"
	now := time.Now().UTC()
	r.RetiredAt = &now
	if err := c.save(r); err != nil {
		r.State = "stopping"
		r.RetiredAt = nil
		return err
	}
	return nil
}

func (c *Controller) host(ctx context.Context) (string, string, error) {
	boot, err := os.ReadFile(filepath.Join(c.procRoot, "sys/kernel/random/boot_id"))
	if err != nil {
		return "", "", err
	}
	out, err := c.run(ctx, "info", "--format", "{{json .ID}}")
	if err != nil {
		return "", "", err
	}
	var id string
	if err := json.Unmarshal(out, &id); err != nil || id == "" || len(boot) == 0 {
		return "", "", errors.New("missing supervisor identity")
	}
	return string(boot), id, nil
}
