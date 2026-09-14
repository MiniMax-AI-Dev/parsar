// Package identity defines execution-service principals independently of product users.
package identity

import (
	"errors"
	"sort"
	"strings"

	"github.com/google/uuid"
)

type ProjectScope struct {
	TenantID       string
	OrganizationID string
	ProjectID      string
}

type Principal struct {
	ProjectScope
	SubjectKind string
	SubjectID   string
}

func (p Principal) Validate() error {
	if _, err := ProjectScopes([]ProjectScope{p.ProjectScope}); err != nil {
		return err
	}
	if (p.SubjectKind != "user" && p.SubjectKind != "service_account") || !validID(p.SubjectID) {
		return errors.New("caller subject_kind must be user or service_account with a nonempty subject_id")
	}
	return nil
}

// ProjectScopes validates the bijection and returns unique scopes in stable lock order.
func ProjectScopes(scopes []ProjectScope) ([]ProjectScope, error) {
	byTenant := make(map[string]ProjectScope, len(scopes))
	byProject := make(map[[2]string]string, len(scopes))
	for _, scope := range scopes {
		tenant, err := uuid.Parse(scope.TenantID)
		if err != nil || tenant == uuid.Nil || tenant.String() != scope.TenantID {
			return nil, errors.New("caller tenant_id must be a canonical nonzero UUID")
		}
		if !validID(scope.OrganizationID) || !validID(scope.ProjectID) {
			return nil, errors.New("caller organization_id and project_id are required")
		}
		project := [2]string{scope.OrganizationID, scope.ProjectID}
		if existing, ok := byTenant[scope.TenantID]; ok && existing != scope {
			return nil, errors.New("caller tenant_id has conflicting project bindings")
		}
		if existing, ok := byProject[project]; ok && existing != scope.TenantID {
			return nil, errors.New("caller project has conflicting tenant bindings")
		}
		byTenant[scope.TenantID], byProject[project] = scope, scope.TenantID
	}
	result := make([]ProjectScope, 0, len(byTenant))
	for _, scope := range byTenant {
		result = append(result, scope)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].TenantID < result[j].TenantID })
	return result, nil
}

func validID(value string) bool { return value != "" && strings.TrimSpace(value) == value }
