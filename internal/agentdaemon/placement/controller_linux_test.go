//go:build linux

package placement

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

const testID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type fixture struct {
	c               *Controller
	unit            *container
	workspace       string
	stops, removals int
	fail            string
	t               *testing.T
}

func writeTestFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(home, ".parsar", "placement-tests")
	if err := os.MkdirAll(base, 0700); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(base, "case-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	f := &fixture{t: t, workspace: filepath.Join(dir, "workspace")}
	if err := os.Mkdir(f.workspace, 0700); err != nil {
		t.Fatal(err)
	}
	f.c = &Controller{root: filepath.Join(dir, "state"), procRoot: filepath.Join(dir, "proc"), cgroupRoot: filepath.Join(dir, "cgroup")}
	f.c.run = f.run
	f.unit = &container{ID: testID, Created: "created-1", Image: "sha256:image"}
	f.unit.State.Running = true
	f.unit.State.Pid = 123
	f.unit.State.StartedAt = "start-1"
	f.unit.Config.User = "1000:1000"
	f.unit.Config.Labels = map[string]string{BindingLabel: "owner-1"}
	f.unit.HostConfig.NetworkMode = "none"
	f.unit.HostConfig.IpcMode = "private"
	f.unit.HostConfig.CgroupnsMode = "private"
	f.unit.HostConfig.CapDrop = []string{"ALL"}
	f.unit.HostConfig.SecurityOpt = []string{"no-new-privileges"}
	f.unit.HostConfig.RestartPolicy.Name = "no"
	f.unit.Mounts = append(f.unit.Mounts, struct {
		Type, Source, Destination, Propagation string
		RW                                     bool
	}{Type: "bind", Source: f.workspace, Destination: "/workspace", Propagation: "rprivate", RW: true})
	writeTestFile(t, filepath.Join(f.c.procRoot, "sys/kernel/random/boot_id"), "boot-1")
	writeTestFile(t, filepath.Join(f.c.procRoot, "self/mountinfo"), "")
	writeTestFile(t, filepath.Join(f.c.procRoot, "123/stat"), "123 (native (worker)) S "+strings.Repeat("0 ", 18)+"999 0")
	writeTestFile(t, filepath.Join(f.c.procRoot, "123/cgroup"), "0::/docker-"+testID+".scope\n")
	writeTestFile(t, filepath.Join(f.c.cgroupRoot, "docker-"+testID+".scope/cgroup.procs"), "123\n")
	writeTestFile(t, filepath.Join(f.c.cgroupRoot, "docker-"+testID+".scope/cgroup.events"), "populated 1\n")
	return f
}

func (f *fixture) run(_ context.Context, args ...string) ([]byte, error) {
	if args[0] == "info" {
		if f.fail == "supervisor" {
			return nil, errors.New("unavailable")
		}
		return []byte(`"supervisor-1"`), nil
	}
	switch args[1] {
	case "inspect":
		if f.unit == nil {
			return nil, os.ErrNotExist
		}
		return json.Marshal([]container{*f.unit})
	case "stop":
		r, err := f.c.load(testID)
		if err != nil || r.State != "stopping" || len(r.Members) == 0 {
			f.t.Fatal("stop before durable intent", err)
		}
		f.stops++
		if f.fail == "stop" {
			return nil, errors.New("stop unavailable")
		}
		f.unit.State.Running = false
		f.unit.State.Pid = 0
		if f.fail != "live" && f.fail != "escaped" {
			_ = os.RemoveAll(filepath.Join(f.c.procRoot, "123"))
			_ = os.RemoveAll(filepath.Join(f.c.cgroupRoot, "docker-"+testID+".scope"))
		}
		if f.fail == "escaped" {
			_ = os.RemoveAll(filepath.Join(f.c.cgroupRoot, "docker-"+testID+".scope"))
		}
		return nil, nil
	case "rm":
		f.removals++
		f.unit = nil
		if f.fail == "lost-remove-ack" {
			return nil, errors.New("controller lost removal acknowledgement")
		}
		return nil, nil
	}
	return nil, errors.New("unexpected Docker command")
}

func (f *fixture) enroll() *Receipt {
	f.t.Helper()
	r, err := f.c.Enroll(context.Background(), testID, "owner-1", f.workspace)
	if err != nil {
		f.t.Fatal(err)
	}
	return r
}

func TestRetirementPersistsBeforeStopAndRecoversIdenticalReceipt(t *testing.T) {
	f := newFixture(t)
	f.enroll()
	writeTestFile(t, filepath.Join(f.workspace, "history"), "retained")
	r, err := f.c.Retire(context.Background(), testID)
	if err != nil || r.State != "retired" {
		t.Fatal(r, err)
	}
	fresh := *f.c
	fresh.run = func(context.Context, ...string) ([]byte, error) {
		t.Fatal("completed receipt must not need supervisor")
		return nil, nil
	}
	again, err := fresh.Retire(context.Background(), testID)
	if err != nil || !reflect.DeepEqual(r, again) {
		t.Fatal("receipt changed across controller restart", err)
	}
	if f.stops != 1 || f.removals != 1 {
		t.Fatal("repeated destructive action")
	}
	if data, err := os.ReadFile(filepath.Join(f.workspace, "history")); err != nil || string(data) != "retained" {
		t.Fatal("history lost")
	}
}

func TestUnknownEvidenceNeverRemovesOrReportsRetired(t *testing.T) {
	for _, failure := range []string{"supervisor", "stop", "live", "escaped", "missing", "restart", "boot", "lost-remove-ack"} {
		t.Run(failure, func(t *testing.T) {
			f := newFixture(t)
			f.enroll()
			f.fail = failure
			switch failure {
			case "missing":
				f.unit = nil
			case "restart":
				f.unit.State.StartedAt = "start-2"
			case "boot":
				writeTestFile(t, filepath.Join(f.c.procRoot, "sys/kernel/random/boot_id"), "boot-2")
			}
			r, err := f.c.Retire(context.Background(), testID)
			if err == nil || r.State == "retired" {
				t.Fatal("uncertain retirement reported success", r, err)
			}
			persisted, err := f.c.load(testID)
			if err != nil || persisted.State == "retired" {
				t.Fatal("uncertainty lost", err)
			}
			if failure != "lost-remove-ack" && f.removals != 0 {
				t.Fatal("removed without proof")
			}
			if failure == "lost-remove-ack" {
				fresh := *f.c
				r, err = fresh.Retire(context.Background(), testID)
				if err == nil || r.State == "retired" {
					t.Fatal("absence manufactured success")
				}
			}
		})
	}
}

func TestReconcileStoppedTargetAfterInterruptedController(t *testing.T) {
	f := newFixture(t)
	r := f.enroll()
	r.State = "stopping"
	if err := f.c.save(r); err != nil {
		t.Fatal(err)
	}
	if _, err := f.run(context.Background(), "container", "stop"); err != nil {
		t.Fatal(err)
	}
	fresh := *f.c
	result, err := fresh.Retire(context.Background(), testID)
	if err != nil || result.State != "retired" || f.stops != 1 {
		t.Fatal(result, err)
	}
}

func TestConcurrentRetirementUsesOneDestructiveAttempt(t *testing.T) {
	f := newFixture(t)
	f.enroll()
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fresh := *f.c
			r, err := fresh.Retire(context.Background(), testID)
			if err != nil || r.State != "retired" {
				t.Error(r, err)
			}
		}()
	}
	wg.Wait()
	if f.stops != 1 || f.removals != 1 {
		t.Fatal("duplicate destructive attempts")
	}
}

func TestTargetLockHonorsCancellation(t *testing.T) {
	f := newFixture(t)
	unlock, err := f.c.lock(context.Background(), testID)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := f.c.lock(ctx, testID); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

func TestEnrollmentRejectsUnqualifiedAuthorityAndProfile(t *testing.T) {
	for _, bad := range []string{"short-id", "wrong-owner", "privileged", "host-pid", "network", "capability", "security", "restart", "workspace", "state-mount", "socket-mount", "relative", "stopped"} {
		t.Run(bad, func(t *testing.T) {
			f := newFixture(t)
			id, owner, workspace := testID, "owner-1", f.workspace
			switch bad {
			case "short-id":
				id = "aaaa"
			case "wrong-owner":
				owner = "someone-else"
			case "privileged":
				f.unit.HostConfig.Privileged = true
			case "host-pid":
				f.unit.HostConfig.PidMode = "host"
			case "network":
				f.unit.HostConfig.NetworkMode = "bridge"
			case "capability":
				f.unit.HostConfig.CapAdd = []string{"SYS_ADMIN"}
			case "security":
				f.unit.HostConfig.SecurityOpt = append(f.unit.HostConfig.SecurityOpt, "seccomp=unconfined")
			case "restart":
				f.unit.HostConfig.RestartPolicy.Name = "always"
			case "workspace":
				f.unit.Mounts = nil
			case "state-mount":
				f.unit.Mounts[0].Source = filepath.Dir(f.c.root)
			case "socket-mount":
				f.unit.Mounts[0].Type = "volume"
			case "relative":
				workspace = "workspace"
			case "stopped":
				f.unit.State.Running = false
			}
			if _, err := f.c.Enroll(context.Background(), id, owner, workspace); err == nil {
				t.Fatal("accepted unqualified enrollment")
			}
			if f.stops+f.removals != 0 {
				t.Fatal("enrollment mutated supervisor")
			}
		})
	}
}

func TestUntrustedStateIsRejected(t *testing.T) {
	f := newFixture(t)
	f.enroll()
	if err := os.Chmod(f.c.recordPath(testID), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.c.Retire(context.Background(), testID); err == nil {
		t.Fatal("accepted exposed authority")
	}
	if f.stops+f.removals != 0 {
		t.Fatal("mutated supervisor before authorization")
	}
}

func TestBindingCannotBeOverwrittenOrAppliedToAnotherContainer(t *testing.T) {
	f := newFixture(t)
	f.enroll()
	if _, err := f.c.Enroll(context.Background(), testID, "owner-1", f.workspace); err == nil {
		t.Fatal("binding overwritten")
	}
	if _, err := f.c.Retire(context.Background(), strings.Repeat("b", 64)); err == nil {
		t.Fatal("another target authorized")
	}
	f.unit.ID = strings.Repeat("b", 64)
	if _, err := f.c.Retire(context.Background(), testID); err == nil {
		t.Fatal("supervisor target substitution accepted")
	}
	if f.stops+f.removals != 0 {
		t.Fatal("wrong target mutated")
	}
}

func TestSymlinkedStateAndChangedProfileAreRejected(t *testing.T) {
	f := newFixture(t)
	f.enroll()
	path := f.c.recordPath(testID)
	if err := os.Rename(path, path+".original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path+".original", path); err != nil {
		t.Fatal(err)
	}
	if _, err := f.c.Retire(context.Background(), testID); err == nil {
		t.Fatal("symlinked authority accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".original", path); err != nil {
		t.Fatal(err)
	}
	f.unit.HostConfig.Privileged = true
	if _, err := f.c.Retire(context.Background(), testID); err == nil {
		t.Fatal("changed profile accepted")
	}
	if f.stops+f.removals != 0 {
		t.Fatal("mutated unqualified placement")
	}
}
