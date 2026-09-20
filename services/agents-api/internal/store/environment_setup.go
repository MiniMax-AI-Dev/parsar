package store

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"regexp"
	"strings"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/credentialcrypto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// EnvironmentSetup is confidential input, never ordinary resource metadata.
// Core freezes it once; the common Runtime initializer executes it in order.
type EnvironmentSetup struct {
	Env      map[string]string      `json:"env,omitempty"`
	Commands []SetupCommand         `json:"setup_commands,omitempty"`
	Packages v1.EnvironmentPackages `json:"packages"`
	Skills   []InlineSkill          `json:"skills,omitempty"`
}

type SetupCommand struct {
	Command string `json:"command"`
	CWD     string `json:"cwd,omitempty"`
}

var environmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (s EnvironmentSetup) Empty() bool {
	return len(s.Env)+len(s.Commands)+len(s.Packages.NPM)+len(s.Packages.Python)+len(s.Packages.System)+len(s.Skills) == 0
}

func (s EnvironmentSetup) Validate() error {
	if ValidateInlineSkills(s.Skills) != nil {
		return ErrInvalidInput
	}
	ordinary := s
	ordinary.Skills = nil
	raw, err := json.Marshal(ordinary)
	if err != nil || len(raw) > 512*1024 {
		return ErrInvalidInput
	}
	for name, value := range s.Env {
		// The first three reservations are explicitly part of the public guide;
		// PARSAR_* identifies the actual deployment authority and binding.
		if !environmentName.MatchString(name) || name == "PATH" || name == "OPENAI_API_KEY" || strings.HasPrefix(name, "CODEX_") || strings.HasPrefix(name, "PARSAR_") || strings.ContainsRune(value, 0) {
			return ErrInvalidInput
		}
	}
	for _, command := range s.Commands {
		if command.Command == "" || strings.ContainsRune(command.Command, 0) || (command.CWD != "" && (!path.IsAbs(command.CWD) || strings.ContainsRune(command.CWD, 0))) {
			return ErrInvalidInput
		}
	}
	for _, packages := range [][]string{s.Packages.NPM, s.Packages.Python, s.Packages.System} {
		for _, item := range packages {
			if item == "" || strings.HasPrefix(item, "-") || strings.ContainsRune(item, 0) {
				return ErrInvalidInput
			}
		}
	}
	return nil
}

func (s *Store) sealEnvironmentSetup(tenant, resource, id, field string, input any, empty bool) ([]byte, error) {
	if empty {
		return nil, nil
	}
	plaintext, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	return s.credentialCipher.SealEnvironmentSetup(plaintext, credentialcrypto.EnvironmentSetupBinding{TenantID: tenant, Resource: resource, OwnerID: id, Field: field})
}

func (s *Store) openEnvironmentSetup(tenant, resource, id, field string, ciphertext []byte, output any) error {
	if len(ciphertext) == 0 {
		return nil
	}
	plaintext, err := s.credentialCipher.OpenEnvironmentSetup(ciphertext, credentialcrypto.EnvironmentSetupBinding{TenantID: tenant, Resource: resource, OwnerID: id, Field: field})
	if err != nil {
		return err
	}
	if json.Unmarshal(plaintext, output) != nil {
		return ErrInvalidInput
	}
	return nil
}

func (s *Store) saveEnvironmentSetup(ctx context.Context, q *sqlc.Queries, tenant string, session pgtype.UUID, setup EnvironmentSetup) error {
	if err := setup.Validate(); err != nil {
		return err
	}
	if setup.Empty() {
		return nil
	}
	owner, err := parseID(tenant)
	if err != nil {
		return err
	}
	encrypted, err := s.sealEnvironmentSetup(uuid.UUID(owner.Bytes).String(), "session", uuid.UUID(session.Bytes).String(), "initialization", setup, false)
	if err != nil {
		return err
	}
	return q.CreateEnvironmentSetup(ctx, sqlc.CreateEnvironmentSetupParams{SessionID: session, Contents: encrypted})
}

func (s *Store) ReadEnvironmentSetup(ctx context.Context, tenant, session string) (EnvironmentSetup, error) {
	var result EnvironmentSetup
	lookup, err := deviceLookup(tenant, session)
	if err != nil {
		return result, ErrNotFound
	}
	encrypted, err := s.queries.GetEnvironmentSetup(ctx, sqlc.GetEnvironmentSetupParams{TenantID: lookup.TenantID, ID: lookup.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return result, ErrNotFound
	}
	if err != nil {
		return result, err
	}
	if err = s.openEnvironmentSetup(uuid.UUID(lookup.TenantID.Bytes).String(), "session", uuid.UUID(lookup.ID.Bytes).String(), "initialization", encrypted, &result); err != nil {
		return result, err
	}
	return result, result.Validate()
}

func (s EnvironmentSetup) PackageMetadata() v1.EnvironmentPackages {
	result := s.Packages
	if result.NPM == nil {
		result.NPM = []string{}
	}
	if result.Python == nil {
		result.Python = []string{}
	}
	if result.System == nil {
		result.System = []string{}
	}
	return result
}

func (s *Store) sealTemplateSetup(tenant, id string, setup EnvironmentSetup) ([]byte, []byte, []byte, error) {
	packages, err := json.Marshal(setup.PackageMetadata())
	if err != nil {
		return nil, nil, nil, err
	}
	env, err := s.sealEnvironmentSetup(tenant, "environment_template", id, "env", setup.Env, len(setup.Env) == 0)
	if err != nil {
		return nil, nil, nil, err
	}
	commands, err := s.sealEnvironmentSetup(tenant, "environment_template", id, "setup_commands", setup.Commands, len(setup.Commands) == 0)
	return packages, env, commands, err
}
