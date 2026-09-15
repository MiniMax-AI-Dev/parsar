package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestVaultsPersistAndStayTenantScoped(t *testing.T) {
	s, pool := testStore(t)
	ctx := context.Background()
	tenantA, tenantB := uuid.NewString(), uuid.NewString()
	before := time.Now().Add(-time.Second)
	unnamed, err := s.CreateVault(ctx, tenantA, CreateVaultInput{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := uuid.Parse(unnamed.ID); err != nil || unnamed.TenantID != tenantA || unnamed.Name != nil || unnamed.Metadata == nil || len(unnamed.Metadata) != 0 || unnamed.CreatedAt.Before(before) || unnamed.CreatedAt.After(time.Now().Add(time.Second)) {
		t.Fatalf("unexpected unnamed vault: %+v, %v", unnamed, err)
	}
	// Validate the byte boundary with multibyte text, without Session metadata
	// count or character limits. Public name trimming belongs to the API layer.
	name := strings.Repeat("é", 128)
	input := CreateVaultInput{Name: &name, Metadata: map[string]string{"": "", "purpose": "保存 configuration"}}
	named, err := s.CreateVault(ctx, tenantA, input)
	if err != nil || named.ID == unnamed.ID || named.Name == nil || *named.Name != name || !reflect.DeepEqual(named.Metadata, input.Metadata) {
		t.Fatalf("unexpected named vault: %+v, %v", named, err)
	}
	for _, tenant := range []string{tenantA, tenantB} {
		other, err := s.CreateVault(ctx, tenant, input)
		if err != nil || other.ID == named.ID || other.TenantID != tenant {
			t.Fatalf("distinct resource creation: %+v, %v", other, err)
		}
	}
	for _, lookup := range []struct{ tenant, id string }{{tenantB, named.ID}, {tenantA, uuid.NewString()}} {
		if _, err := s.GetVault(ctx, lookup.tenant, lookup.id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unowned/unknown vault lookup: %v", err)
		}
	}
	// Recreate the pool and Store as a restarted standalone service would.
	pool.Close()
	reopened, _ := testStore(t)
	for _, want := range []Vault{unnamed, named} {
		got, err := reopened.GetVault(ctx, tenantA, want.ID)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("durable read: %+v, %v; want %+v", got, err, want)
		}
	}
	var sessions int
	if err := reopened.pool.QueryRow(ctx, "SELECT count(*) FROM sessions WHERE tenant_id = $1", tenantA).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("Vault creation produced %d Sessions: %v", sessions, err)
	}
}

func TestVaultsRejectInvalidStoreInputWithoutWrites(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	tenant := uuid.NewString()
	for _, name := range []string{"", strings.Repeat("x", 257), strings.Repeat("é", 129), string([]byte{0xff})} {
		if _, err := s.CreateVault(ctx, tenant, CreateVaultInput{Name: &name}); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid name length %d: %v", len(name), err)
		}
	}
	for _, invalid := range []string{"", "not-a-uuid", uuid.Nil.String()} {
		if _, err := s.CreateVault(ctx, invalid, CreateVaultInput{}); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid create tenant accepted: %v", err)
		}
		if _, err := s.GetVault(ctx, invalid, uuid.NewString()); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid read tenant accepted: %v", err)
		}
		if _, err := s.GetVault(ctx, tenant, invalid); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid vault ID accepted: %v", err)
		}
	}
	if _, err := s.CreateVault(ctx, tenant, CreateVaultInput{Metadata: map[string]string{"large": strings.Repeat("x", 64*1024)}}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("oversized metadata accepted: %v", err)
	}
	var count int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM vaults WHERE tenant_id = $1", tenant).Scan(&count); err != nil || count != 0 {
		t.Fatalf("invalid input created %d Vaults: %v", count, err)
	}
}
