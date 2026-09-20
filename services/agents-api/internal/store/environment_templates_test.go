package store

import (
	"errors"
	"github.com/google/uuid"
	"sync"
	"testing"
)

func TestEnvironmentTemplatesDurabilityIsolationAndConcurrentUpdates(t *testing.T) {
	s, pool := testStore(t)
	ctx := t.Context()
	tenant, foreign := uuid.NewString(), uuid.NewString()
	name := " template "
	created, err := s.CreateEnvironmentTemplate(ctx, tenant, EnvironmentTemplateInput{Name: &name})
	if err != nil || created.NetworkAccess != "enabled" || created.Name == nil || *created.Name != name || !created.CreatedAt.Equal(created.UpdatedAt) {
		t.Fatal(created, err)
	}
	for _, target := range []string{foreign} {
		if _, err := s.GetEnvironmentTemplate(ctx, target, created.ID); !errors.Is(err, ErrNotFound) {
			t.Fatal("foreign read", err)
		}
		if _, err := s.UpdateEnvironmentTemplate(ctx, target, created.ID, EnvironmentTemplateInput{SetName: true}); !errors.Is(err, ErrNotFound) {
			t.Fatal("foreign update", err)
		}
		if _, err := s.DeleteEnvironmentTemplate(ctx, target, created.ID); !errors.Is(err, ErrNotFound) {
			t.Fatal("foreign delete", err)
		}
		if _, err := s.ListEnvironmentTemplates(ctx, target, created.ID, 1, false); !errors.Is(err, ErrNotFound) {
			t.Fatal("foreign cursor", err)
		}
	}
	newName := "changed"
	var wg sync.WaitGroup
	for _, in := range []EnvironmentTemplateInput{{Name: &newName, SetName: true}, {NetworkAccess: "disabled", SetNetwork: true}} {
		wg.Add(1)
		go func(in EnvironmentTemplateInput) {
			defer wg.Done()
			if _, err := s.UpdateEnvironmentTemplate(ctx, tenant, created.ID, in); err != nil {
				t.Error(err)
			}
		}(in)
	}
	wg.Wait()
	got, err := s.GetEnvironmentTemplate(ctx, tenant, created.ID)
	if err != nil || got.NetworkAccess != "disabled" || got.Name == nil || *got.Name != newName || !got.CreatedAt.Equal(created.CreatedAt) {
		t.Fatal("lost concurrent update", got, err)
	}
	pool.Close()
	s, _ = testStore(t)
	got, err = s.GetEnvironmentTemplate(ctx, tenant, created.ID)
	if err != nil || got.NetworkAccess != "disabled" {
		t.Fatal("lost durable template", got, err)
	}
	ids := []string{created.ID}
	for range 3 {
		v, err := s.CreateEnvironmentTemplate(ctx, tenant, EnvironmentTemplateInput{})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, v.ID)
	}
	first, err := s.ListEnvironmentTemplates(ctx, tenant, "", 2, true)
	if err != nil || !first.HasMore || len(first.Templates) != 2 || first.Templates[0].ID != ids[0] {
		t.Fatal(first, err)
	}
	second, err := s.ListEnvironmentTemplates(ctx, tenant, first.Templates[1].ID, 2, true)
	if err != nil || second.HasMore || len(second.Templates) != 2 || second.Templates[0].ID != ids[2] {
		t.Fatal(second, err)
	}
	reverse, err := s.ListEnvironmentTemplates(ctx, tenant, "", 1, false)
	if err != nil || reverse.Templates[0].ID != ids[3] {
		t.Fatal(reverse, err)
	}
	cleared, err := s.UpdateEnvironmentTemplate(ctx, tenant, created.ID, EnvironmentTemplateInput{SetName: true, SetNetwork: true, NetworkAccess: "enabled"})
	if err != nil || cleared.Name != nil || cleared.NetworkAccess != "enabled" {
		t.Fatal(cleared, err)
	}
	if id, err := s.DeleteEnvironmentTemplate(ctx, tenant, created.ID); err != nil || id != created.ID {
		t.Fatal(id, err)
	}
	if _, err := s.GetEnvironmentTemplate(ctx, tenant, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := s.CreateEnvironmentTemplate(ctx, tenant, EnvironmentTemplateInput{SetNetwork: true, NetworkAccess: "restricted"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatal(err)
	}
}
