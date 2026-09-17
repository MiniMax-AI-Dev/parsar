//go:build linux

package placement

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"sync"
	"testing"
)

const testEnvironment = "3fb4bdd9-d8c7-4f12-810c-9da932e06bc4"
const otherEnvironment = "384ff427-c83f-4484-80a7-58aa95662f97"

func (f *fixture) enrollEnvironment() *Receipt {
	f.t.Helper()
	r, err := f.c.EnrollEnvironment(context.Background(), testID, "owner-1", f.workspace, testEnvironment)
	if err != nil {
		f.t.Fatal(err)
	}
	return r
}

func TestEnvironmentScopeRejectsBeforeSupervisorAccess(t *testing.T) {
	for _, state := range []string{"enrolled", "retired"} {
		t.Run(state, func(t *testing.T) {
			f := newFixture(t)
			f.enrollEnvironment()
			if state == "retired" {
				if _, err := f.c.RetireEnvironment(t.Context(), testID, testEnvironment); err != nil {
					t.Fatal(err)
				}
			}
			f.c.run = func(context.Context, ...string) ([]byte, error) {
				t.Fatal("scope rejection accessed supervisor")
				return nil, nil
			}
			if _, err := f.c.Retire(t.Context(), testID); err == nil {
				t.Fatal("scope omitted")
			}
			for _, id := range []string{otherEnvironment, "", "not-a-uuid", "00000000-0000-0000-0000-000000000000"} {
				if _, err := f.c.RetireEnvironment(t.Context(), testID, id); err == nil {
					t.Fatal("invalid scope accepted", id)
				}
			}
		})
	}
}

func TestEnvironmentEnrollmentIsImmutableAndSeparateFromLegacy(t *testing.T) {
	f := newFixture(t)
	r := f.enrollEnvironment()
	if r.Version != 2 || r.EnvironmentID != testEnvironment {
		t.Fatal("scope not versioned", r)
	}
	if _, err := f.c.EnrollEnvironment(t.Context(), testID, "owner-1", f.workspace, otherEnvironment); err == nil {
		t.Fatal("scope reassigned")
	}
	if _, err := f.c.Enroll(t.Context(), testID, "owner-1", f.workspace); err == nil {
		t.Fatal("scope downgraded")
	}
	retained, err := f.c.load(testID)
	if err != nil || !reflect.DeepEqual(r, retained) {
		t.Fatal("enrollment changed", err)
	}
	legacy := newFixture(t)
	old := legacy.enroll()
	if old.Version != 1 || old.EnvironmentID != "" {
		t.Fatal("legacy enrollment changed")
	}
	if _, err := legacy.c.RetireEnvironment(t.Context(), testID, testEnvironment); err == nil {
		t.Fatal("legacy record gained scope")
	}
	if legacy.stops+legacy.removals != 0 {
		t.Fatal("legacy target mutated")
	}
	if _, err := legacy.c.Retire(t.Context(), testID); err != nil {
		t.Fatal(err)
	}
}

func TestEnvironmentReceiptVersionRejectsMissingOrDowngradedScope(t *testing.T) {
	for _, mode := range []string{"missing", "malformed", "downgraded", "future"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			r := f.enrollEnvironment()
			switch mode {
			case "missing":
				r.EnvironmentID = ""
			case "malformed":
				r.EnvironmentID = "invalid"
			case "downgraded":
				r.Version = 1
			case "future":
				r.Version = 3
			}
			data, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(f.c.recordPath(testID), data, 0600); err != nil {
				t.Fatal(err)
			}
			f.c.run = func(context.Context, ...string) ([]byte, error) {
				t.Fatal("invalid receipt accessed supervisor")
				return nil, nil
			}
			if _, err := f.c.RetireEnvironment(t.Context(), testID, testEnvironment); err == nil {
				t.Fatal("invalid scoped receipt accepted")
			}
			if _, err := f.c.Retire(t.Context(), testID); err == nil {
				t.Fatal("invalid scoped receipt accepted by legacy path")
			}
		})
	}
}

func TestEnvironmentRetirementConcurrentRecovery(t *testing.T) {
	f := newFixture(t)
	f.enrollEnvironment()
	var wg sync.WaitGroup
	results := make(chan *Receipt, 4)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fresh := *f.c
			r, err := fresh.RetireEnvironment(t.Context(), testID, testEnvironment)
			if err != nil {
				t.Error(err)
				return
			}
			results <- r
		}()
	}
	wg.Wait()
	close(results)
	var expected *Receipt
	for r := range results {
		if expected == nil {
			expected = r
		}
		if !reflect.DeepEqual(expected, r) {
			t.Fatal("concurrent receipt changed")
		}
	}
	if expected == nil || expected.State != "retired" || expected.EnvironmentID != testEnvironment || f.stops != 1 || f.removals != 1 {
		t.Fatal("retirement not unique", expected)
	}
	fresh := *f.c
	fresh.run = func(context.Context, ...string) ([]byte, error) {
		t.Fatal("recovered receipt touched supervisor")
		return nil, nil
	}
	again, err := fresh.RetireEnvironment(t.Context(), testID, testEnvironment)
	if err != nil || !reflect.DeepEqual(expected, again) {
		t.Fatal("fresh recovery differs", err)
	}
}

func TestEnvironmentUnknownAndChangedTargetRemainUnresolved(t *testing.T) {
	for _, failure := range []string{"lost-remove-ack", "restart"} {
		t.Run(failure, func(t *testing.T) {
			f := newFixture(t)
			f.enrollEnvironment()
			f.fail = failure
			if failure == "restart" {
				f.unit.State.StartedAt = "replacement"
			}
			if r, err := f.c.RetireEnvironment(t.Context(), testID, testEnvironment); err == nil || r.State == "retired" {
				t.Fatal("uncertainty lost", r, err)
			}
			fresh := *f.c
			if r, err := fresh.RetireEnvironment(t.Context(), testID, testEnvironment); err == nil || r.State == "retired" {
				t.Fatal("retry fabricated retirement", r, err)
			}
			if _, err := fresh.Retire(t.Context(), testID); err == nil {
				t.Fatal("unscoped retry bypassed unknown scope")
			}
		})
	}
}

func TestInvalidEnvironmentEnrollmentDoesNotTouchSupervisor(t *testing.T) {
	f := newFixture(t)
	f.c.run = func(context.Context, ...string) ([]byte, error) {
		t.Fatal("invalid scope touched supervisor")
		return nil, errors.New("unexpected")
	}
	for _, id := range []string{"", "3FB4BDD9-D8C7-4F12-810C-9DA932E06BC4", "00000000-0000-0000-0000-000000000000"} {
		if _, err := f.c.EnrollEnvironment(t.Context(), testID, "owner-1", f.workspace, id); err == nil {
			t.Fatal("invalid scope enrolled")
		}
	}
}
