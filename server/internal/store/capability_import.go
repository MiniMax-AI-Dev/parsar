// Capability import: built_in=TRUE credential_kinds rows are immutable here
// (seeded by migration). ImportCapability is one tx so a secret write can't
// outlive a failed capability_version write — handler does AES-GCM envelope
// encryption before calling; the store never sees the master key.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

// CredentialKindRead is the public shape returned by credential_kinds APIs.
// CreatedBy is "" for built_in seed rows (SQL coalesces NULL → "").
type CredentialKindRead struct {
	ID          string         `json:"id"`
	Code        string         `json:"code"`
	DisplayName string         `json:"display_name"`
	Description string         `json:"description"`
	ValueSchema map[string]any `json:"value_schema"`
	BuiltIn     bool           `json:"built_in"`
	Source      string         `json:"source"`
	CreatedBy   string         `json:"created_by,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	Metadata    map[string]any `json:"-"`
}

// CredentialKindSource enum — see migration 000009.
const (
	CredentialKindSourcePlatformOAuth = "platform_oauth"
	CredentialKindSourcePlatformModel = "platform_model"
	CredentialKindSourceUserDefined   = "user_defined"
)

// CreateCredentialKindInput is the parameter struct for the inline-create
// path. Code is normalized to lowercase + trimmed; uniqueness is enforced by
// the partial unique index uk_credential_kinds_code_active.
type CreateCredentialKindInput struct {
	Code        string
	DisplayName string
	Description string
	Source      string // empty defaults to user_defined
	ValueSchema map[string]any
	CreatorID   string
}

// ErrCredentialKindNotFound is returned when no active credential_kinds row matches.
var ErrCredentialKindNotFound = errors.New("store: credential kind not found")

// ErrCredentialKindDuplicate is returned on unique-violation against the code column.
var ErrCredentialKindDuplicate = errors.New("store: credential kind code already exists")

// ErrCapabilityKindMismatch is returned when Spec.Kind disagrees with the existing
// capability.type. Handlers map this to HTTP 422.
var ErrCapabilityKindMismatch = errors.New("store: spec kind does not match capability type")

// ErrCapabilityNameTaken is returned by ImportCapability when the
// (workspace_id, name) pair already exists on an active capability row.
// Handlers map this to HTTP 409 with a user-facing message that tells
// them how to fix it (rename in source manifest, or delete the existing
// capability first).
var ErrCapabilityNameTaken = errors.New("store: capability name already in use in this workspace")

// ListCredentialKinds returns every active credential_kinds row, built-ins
// first, then user-created in code order.
func (s *Store) ListCredentialKinds(ctx context.Context) ([]CredentialKindRead, error) {
	rows, err := sqlc.New(s.db).ListCredentialKinds(ctx)
	if err != nil {
		return nil, fmt.Errorf("list credential_kinds: %w", err)
	}
	out := make([]CredentialKindRead, 0, len(rows))
	for _, row := range rows {
		out = append(out, credentialKindFromListRow(row))
	}
	return out, nil
}

// GetCredentialKindByCode returns one active credential_kinds row by code.
// Returns ErrCredentialKindNotFound when no active row matches.
func (s *Store) GetCredentialKindByCode(ctx context.Context, code string) (CredentialKindRead, error) {
	normalized := strings.ToLower(strings.TrimSpace(code))
	if normalized == "" {
		return CredentialKindRead{}, fmt.Errorf("%w: empty code", ErrCredentialKindNotFound)
	}
	row, err := sqlc.New(s.db).GetCredentialKindByCode(ctx, normalized)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CredentialKindRead{}, fmt.Errorf("%w: %s", ErrCredentialKindNotFound, normalized)
		}
		return CredentialKindRead{}, fmt.Errorf("get credential_kind %q: %w", normalized, err)
	}
	return credentialKindFromGetRow(row), nil
}

// CreateCredentialKind inserts a non-built-in credential_kinds row. built_in
// rows come from the seed migration, never this path.
func (s *Store) CreateCredentialKind(ctx context.Context, input CreateCredentialKindInput) (CredentialKindRead, error) {
	code := strings.ToLower(strings.TrimSpace(input.Code))
	if code == "" {
		return CredentialKindRead{}, fmt.Errorf("credential_kind: code is required")
	}
	displayName := strings.TrimSpace(input.DisplayName)
	if displayName == "" {
		return CredentialKindRead{}, fmt.Errorf("credential_kind: display_name is required")
	}
	source := strings.TrimSpace(input.Source)
	if source == "" {
		source = CredentialKindSourceUserDefined
	}
	switch source {
	case CredentialKindSourcePlatformModel, CredentialKindSourceUserDefined:
		// allowed via API
	case CredentialKindSourcePlatformOAuth:
		// platform_oauth must come from a migration that also ships the
		// OAuth handler routes; refuse the API path so an admin cannot
		// create a kind that the OAuth flow does not actually handle.
		return CredentialKindRead{}, fmt.Errorf("credential_kind: source %q is reserved for built-in seeds", source)
	default:
		return CredentialKindRead{}, fmt.Errorf("credential_kind: invalid source %q", source)
	}
	creatorID, err := uuid(input.CreatorID)
	if err != nil {
		return CredentialKindRead{}, fmt.Errorf("credential_kind: invalid creator_id: %w", err)
	}
	schemaJSON, err := json.Marshal(nonNilMap(input.ValueSchema))
	if err != nil {
		return CredentialKindRead{}, fmt.Errorf("credential_kind: marshal value_schema: %w", err)
	}
	now := time.Now().UTC()
	row, err := sqlc.New(s.db).CreateCredentialKind(ctx, sqlc.CreateCredentialKindParams{
		ID:          mustUUID(newID()),
		Code:        code,
		DisplayName: displayName,
		Description: strings.TrimSpace(input.Description),
		ValueSchema: schemaJSON,
		Source:      source,
		CreatedBy:   creatorID,
		Now:         timestamptz(now),
	})
	if err != nil {
		if isUniqueViolation(err) {
			return CredentialKindRead{}, fmt.Errorf("%w: %s", ErrCredentialKindDuplicate, code)
		}
		return CredentialKindRead{}, fmt.Errorf("create credential_kind: %w", err)
	}
	return credentialKindFromCreateRow(row), nil
}

// ImportCapabilityInput drives the transactional capability import.
//
// Spec is consumed in-place: inline_secret entries with empty SecretID get
// patched with the secret_id written inside the tx, so callers must treat Spec
// as moved into the store.
//
// Each InlineSecrets entry MUST match exactly one Spec env entry (by
// ServerName + EnvKey) that has mode=inline_secret AND empty SecretID.
type ImportCapabilityInput struct {
	WorkspaceID   string
	Name          string
	Description   string
	Visibility    string
	Type          string
	CreatorID     string
	Version       string
	SourcePayload json.RawMessage // raw user paste, jsonb {"format":"…","body":"…"}
	Spec          canonical.Spec
	InlineSecrets []ImportInlineSecret
	// OssKey / SHA256 carry the storage breadcrumb for any
	// OSS-zip-backed capability (skill + plugin). Empty for mcp /
	// markdown-paste skills.
	OssKey string
	SHA256 string
}

// ImportInlineSecret describes one cleartext value the handler has already
// encrypted; the store writes it to the secrets table and feeds the
// resulting secret_id back into Spec.
type ImportInlineSecret struct {
	ServerName       string
	EnvKey           string
	SecretName       string
	EncryptedPayload []byte // AES-GCM envelope from secrets.Service.Encrypt
	KeyVersion       string
}

// ImportCapabilityResult bundles everything the HTTP handler needs to render
// the post-commit response: the new capability + its first version + the IDs
// of any secret rows materialized along the way.
type ImportCapabilityResult struct {
	Capability        CapabilityRead
	CapabilityVersion CapabilityVersionRead
	CreatedSecretIDs  []string
}

// ImportCapability runs the all-or-nothing create-capability + first-version
// import flow. Any failure rolls back the tx so orphan secrets cannot survive.
func (s *Store) ImportCapability(ctx context.Context, input ImportCapabilityInput) (ImportCapabilityResult, error) {
	workspaceID, err := uuid(input.WorkspaceID)
	if err != nil {
		return ImportCapabilityResult{}, fmt.Errorf("import_capability: workspace_id: %w", err)
	}
	creatorID, err := uuid(input.CreatorID)
	if err != nil {
		return ImportCapabilityResult{}, fmt.Errorf("import_capability: creator_id: %w", err)
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return ImportCapabilityResult{}, fmt.Errorf("import_capability: name is required")
	}
	// Loose pre-check: inline_secret entries arrive with empty SecretID by
	// design and are patched inside the tx; strict Validate() runs post-patch.
	if err := validateImportSpecPreCommit(input.Spec); err != nil {
		return ImportCapabilityResult{}, fmt.Errorf("import_capability: spec invalid: %w", err)
	}

	// Resolve credential_ref kinds before opening tx so a missing kind doesn't
	// roll back a half-built capability.
	credentialKinds, err := s.collectAndValidateCredentialRefs(ctx, input.Spec)
	if err != nil {
		return ImportCapabilityResult{}, err
	}

	tx, err := beginTx(ctx, s.db)
	if err != nil {
		return ImportCapabilityResult{}, fmt.Errorf("import_capability: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New(tx)

	now := time.Now().UTC()

	capabilityID := mustUUID(newID())
	capRow, err := q.CreateCapability(ctx, sqlc.CreateCapabilityParams{
		ID:          capabilityID,
		WorkspaceID: workspaceID,
		Type:        normalizeCapabilityType(input.Type),
		Name:        name,
		Description: strings.TrimSpace(input.Description),
		Visibility:  normalizeCapabilityVisibility(input.Visibility),
		// status column retired as a control signal; insert "active"
		// unconditionally. See store/capabilities.go CreateCapability
		// for the same rationale.
		Status:    "active",
		CreatorID: creatorID,
		Now:       timestamptz(now),
	})
	if err != nil {
		if isUniqueViolation(err) {
			return ImportCapabilityResult{}, fmt.Errorf("%w: %s", ErrCapabilityNameTaken, name)
		}
		return ImportCapabilityResult{}, fmt.Errorf("import_capability: create capability: %w", err)
	}

	versionRead, createdSecretIDs, err := commitCapabilityVersionInTx(ctx, q, commitVersionParams{
		WorkspaceID:         workspaceID,
		CapabilityID:        capabilityID,
		CapabilityName:      capRow.ID,
		CreatorID:           creatorID,
		Version:             input.Version,
		SourcePayload:       input.SourcePayload,
		Spec:                &input.Spec,
		InlineSecrets:       input.InlineSecrets,
		RequiredCredentials: credentialKinds,
		OssKey:              input.OssKey,
		SHA256:              input.SHA256,
		Now:                 now,
	})
	if err != nil {
		return ImportCapabilityResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return ImportCapabilityResult{}, fmt.Errorf("import_capability: commit: %w", err)
	}

	capability := capabilityFromCreateRow(capRow)
	capability.RequiredCredentials = versionRead.RequiredCredentials
	return ImportCapabilityResult{
		Capability:        capability,
		CapabilityVersion: versionRead,
		CreatedSecretIDs:  createdSecretIDs,
	}, nil
}

// ImportCapabilityVersionInput drives adding a new version to an existing
// capability. Name/Description/Visibility/Type come from the existing row;
// Spec.Kind must match capability.type or the call returns ErrCapabilityKindMismatch.
type ImportCapabilityVersionInput struct {
	WorkspaceID   string
	CapabilityID  string
	CreatorID     string
	Version       string
	SourcePayload json.RawMessage
	Spec          canonical.Spec
	InlineSecrets []ImportInlineSecret
	OssKey        string
	SHA256        string
}

// ImportCapabilityVersion is the version-only analogue of ImportCapability.
//
// Cross-workspace capabilities are surfaced as ErrUnknownCapability so existence
// doesn't leak. Kind mismatches return ErrCapabilityKindMismatch.
func (s *Store) ImportCapabilityVersion(ctx context.Context, input ImportCapabilityVersionInput) (ImportCapabilityResult, error) {
	workspaceID, err := uuid(input.WorkspaceID)
	if err != nil {
		return ImportCapabilityResult{}, fmt.Errorf("import_capability_version: workspace_id: %w", err)
	}
	creatorID, err := uuid(input.CreatorID)
	if err != nil {
		return ImportCapabilityResult{}, fmt.Errorf("import_capability_version: creator_id: %w", err)
	}
	capabilityID, err := uuid(input.CapabilityID)
	if err != nil {
		return ImportCapabilityResult{}, fmt.Errorf("import_capability_version: capability_id: %w", err)
	}
	if err := validateImportSpecPreCommit(input.Spec); err != nil {
		return ImportCapabilityResult{}, fmt.Errorf("import_capability_version: spec invalid: %w", err)
	}

	// Fail fast on missing/foreign capability before opening the tx.
	existing, err := s.GetCapability(ctx, input.CapabilityID)
	if err != nil {
		return ImportCapabilityResult{}, err
	}
	if existing.WorkspaceID != input.WorkspaceID {
		// Surface cross-workspace as not-found so existence doesn't leak.
		return ImportCapabilityResult{}, fmt.Errorf("%w: %s", ErrUnknownCapability, input.CapabilityID)
	}
	if !capabilityKindMatchesType(input.Spec.Kind, existing.Type) {
		return ImportCapabilityResult{}, fmt.Errorf("%w: spec kind %q vs capability type %q", ErrCapabilityKindMismatch, input.Spec.Kind, existing.Type)
	}

	credentialKinds, err := s.collectAndValidateCredentialRefs(ctx, input.Spec)
	if err != nil {
		return ImportCapabilityResult{}, err
	}

	tx, err := beginTx(ctx, s.db)
	if err != nil {
		return ImportCapabilityResult{}, fmt.Errorf("import_capability_version: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New(tx)

	now := time.Now().UTC()

	versionRead, createdSecretIDs, err := commitCapabilityVersionInTx(ctx, q, commitVersionParams{
		WorkspaceID:         workspaceID,
		CapabilityID:        capabilityID,
		CapabilityName:      existing.ID,
		CreatorID:           creatorID,
		Version:             input.Version,
		SourcePayload:       input.SourcePayload,
		Spec:                &input.Spec,
		InlineSecrets:       input.InlineSecrets,
		RequiredCredentials: credentialKinds,
		OssKey:              input.OssKey,
		SHA256:              input.SHA256,
		Now:                 now,
	})
	if err != nil {
		return ImportCapabilityResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return ImportCapabilityResult{}, fmt.Errorf("import_capability_version: commit: %w", err)
	}

	existing.RequiredCredentials = versionRead.RequiredCredentials
	return ImportCapabilityResult{
		Capability:        existing,
		CapabilityVersion: versionRead,
		CreatedSecretIDs:  createdSecretIDs,
	}, nil
}

// commitVersionParams bundles inputs for commitCapabilityVersionInTx. Spec is
// a pointer because SecretID is patched in-place after inline-secret writes.
type commitVersionParams struct {
	WorkspaceID         pgtype.UUID
	CapabilityID        pgtype.UUID
	CapabilityName      string // capability_id for secret metadata breadcrumb
	CreatorID           pgtype.UUID
	Version             string
	SourcePayload       json.RawMessage
	Spec                *canonical.Spec
	InlineSecrets       []ImportInlineSecret
	RequiredCredentials []RequiredCredential
	OssKey              string
	SHA256              string
	Now                 time.Time
}

// commitCapabilityVersionInTx writes inline_secrets (patching SecretID into Spec),
// re-runs strict Spec.Validate() now that every SecretID is filled, then inserts
// capability_version. Caller owns the tx lifecycle.
func commitCapabilityVersionInTx(ctx context.Context, q *sqlc.Queries, p commitVersionParams) (CapabilityVersionRead, []string, error) {
	createdSecretIDs := make([]string, 0, len(p.InlineSecrets))
	for i, secret := range p.InlineSecrets {
		secretName := strings.TrimSpace(secret.SecretName)
		if secretName == "" {
			return CapabilityVersionRead{}, nil, fmt.Errorf("import_capability: inline_secrets[%d]: secret_name is required", i)
		}
		if len(secret.EncryptedPayload) == 0 {
			return CapabilityVersionRead{}, nil, fmt.Errorf("import_capability: inline_secrets[%d] (%s.%s): encrypted_payload is empty", i, secret.ServerName, secret.EnvKey)
		}
		metaJSON, err := json.Marshal(map[string]any{
			"origin":        "capability_import",
			"capability_id": p.CapabilityName,
			"server":        secret.ServerName,
			"env_key":       secret.EnvKey,
		})
		if err != nil {
			return CapabilityVersionRead{}, nil, fmt.Errorf("import_capability: marshal secret metadata: %w", err)
		}
		keyVersion := strings.TrimSpace(secret.KeyVersion)
		if keyVersion == "" {
			keyVersion = "v1"
		}
		secretRow, err := q.CreateSecret(ctx, sqlc.CreateSecretParams{
			ID:               mustUUID(newID()),
			Slug:             generateAutoSlug("secret"),
			Name:             secretName,
			Kind:             "capability_inline",
			Provider:         "inline",
			AuthType:         "literal",
			EncryptedPayload: secret.EncryptedPayload,
			KeyVersion:       keyVersion,
			Metadata:         metaJSON,
			CreatedBy:        p.CreatorID,
			Now:              timestamptz(p.Now),
		})
		if err != nil {
			if isUniqueViolation(err) {
				return CapabilityVersionRead{}, nil, fmt.Errorf("import_capability: inline_secrets[%d]: slug collision for secret %q (auto-generated, retry)", i, secretName)
			}
			return CapabilityVersionRead{}, nil, fmt.Errorf("import_capability: create secret %q: %w", secretName, err)
		}
		createdSecretIDs = append(createdSecretIDs, secretRow.ID)
		if err := patchInlineSecretID(p.Spec, secret.ServerName, secret.EnvKey, secretRow.ID); err != nil {
			return CapabilityVersionRead{}, nil, fmt.Errorf("import_capability: patch inline_secret[%d]: %w", i, err)
		}
	}

	if err := assertInlineSecretsResolved(*p.Spec); err != nil {
		return CapabilityVersionRead{}, nil, fmt.Errorf("import_capability: %w", err)
	}
	if err := p.Spec.Validate(); err != nil {
		return CapabilityVersionRead{}, nil, fmt.Errorf("import_capability: post-patch spec invalid: %w", err)
	}

	specJSON, err := json.Marshal(p.Spec)
	if err != nil {
		return CapabilityVersionRead{}, nil, fmt.Errorf("import_capability: marshal canonical_spec: %w", err)
	}
	requiredCredentialsJSON, err := encodeRequiredCredentials(p.RequiredCredentials)
	if err != nil {
		return CapabilityVersionRead{}, nil, fmt.Errorf("import_capability: encode required_credentials: %w", err)
	}
	version := strings.TrimSpace(p.Version)
	if version == "" {
		version = "1.0.0"
	}
	schemaVersion := p.Spec.SchemaVersion
	if schemaVersion <= 0 {
		schemaVersion = canonical.SchemaVersionCurrent
	}
	contentJSON, err := json.Marshal(map[string]any{})
	if err != nil {
		return CapabilityVersionRead{}, nil, fmt.Errorf("import_capability: marshal empty content: %w", err)
	}
	// oss_key / sha256 are authoritative columns for OSS-zip-backed
	// capabilities (skill + plugin). Caller-supplied wins; fall back
	// to plugin spec for callers that haven't migrated yet.
	ossKey := strings.TrimSpace(p.OssKey)
	sha256 := strings.ToLower(strings.TrimSpace(p.SHA256))
	if ossKey == "" && p.Spec != nil && p.Spec.Kind == canonical.KindPlugin && p.Spec.Plugin != nil {
		ossKey = strings.TrimSpace(p.Spec.Plugin.OssKey)
		sha256 = strings.ToLower(strings.TrimSpace(p.Spec.Plugin.SHA256))
	}
	versionRow, err := q.CreateCapabilityVersion(ctx, sqlc.CreateCapabilityVersionParams{
		ID:                  mustUUID(newID()),
		CapabilityID:        p.CapabilityID,
		Version:             version,
		GitRepoUrl:          pgtype.Text{},
		GitRef:              pgtype.Text{},
		Path:                pgtype.Text{},
		Content:             contentJSON,
		SourcePayload:       []byte(p.SourcePayload),
		SchemaVersion:       schemaVersion,
		CanonicalSpec:       specJSON,
		RequiredCredentials: requiredCredentialsJSON,
		OssKey:              ossKey,
		Sha256:              sha256,
		CreatorID:           p.CreatorID,
		Now:                 timestamptz(p.Now),
	})
	if err != nil {
		return CapabilityVersionRead{}, nil, fmt.Errorf("import_capability: create capability_version: %w", err)
	}
	return capabilityVersionFromCreateRow(versionRow), createdSecretIDs, nil
}

// capabilityKindMatchesType compares canonical.Kind against capability.type
// (both use the same string codes: "mcp", "skill", "plugin").
func capabilityKindMatchesType(kind canonical.Kind, capType string) bool {
	return strings.EqualFold(string(kind), strings.TrimSpace(capType))
}

// collectAndValidateCredentialRefs returns a deduplicated []RequiredCredential
// for each distinct credential_ref kind code in the spec, erroring if any kind
// is missing from credential_kinds.
func (s *Store) collectAndValidateCredentialRefs(ctx context.Context, spec canonical.Spec) ([]RequiredCredential, error) {
	if spec.MCP == nil {
		return nil, nil
	}
	seen := make(map[string]struct{})
	var out []RequiredCredential
	for _, srv := range spec.MCP.Servers {
		for envKey, value := range srv.Env {
			if value.Mode != canonical.EnvModeCredentialRef {
				continue
			}
			code := strings.ToLower(strings.TrimSpace(value.CredentialKindCode))
			if code == "" {
				return nil, fmt.Errorf("import_capability: server %q env %q: credential_ref missing credential_kind_code", srv.Name, envKey)
			}
			if _, dup := seen[code]; dup {
				continue
			}
			if _, err := s.GetCredentialKindByCode(ctx, code); err != nil {
				return nil, fmt.Errorf("import_capability: server %q env %q references unknown credential_kind %q: %w", srv.Name, envKey, code, err)
			}
			seen[code] = struct{}{}
			out = append(out, RequiredCredential{Kind: code, Required: true})
		}
	}
	return out, nil
}

// patchInlineSecretID updates Spec.MCP.Servers[*].Env[envKey].SecretID for the
// matching server. Errors if the server or env entry is missing, or if the
// target slot is not in inline_secret mode.
func patchInlineSecretID(spec *canonical.Spec, serverName, envKey, secretID string) error {
	if spec.MCP == nil {
		return fmt.Errorf("spec.MCP is nil (kind=%s)", spec.Kind)
	}
	for i, srv := range spec.MCP.Servers {
		if srv.Name != serverName {
			continue
		}
		value, ok := srv.Env[envKey]
		if !ok {
			return fmt.Errorf("server %q has no env entry %q", serverName, envKey)
		}
		if value.Mode != canonical.EnvModeInlineSecret {
			return fmt.Errorf("server %q env %q is not in inline_secret mode (got %q)", serverName, envKey, value.Mode)
		}
		if value.SecretID != "" {
			return fmt.Errorf("server %q env %q already has secret_id %q", serverName, envKey, value.SecretID)
		}
		value.SecretID = secretID
		// EnvValue is map-valued; reassign to persist the mutation.
		spec.MCP.Servers[i].Env[envKey] = value
		return nil
	}
	return fmt.Errorf("server %q not found in spec", serverName)
}

// assertInlineSecretsResolved verifies no EnvValue with mode=inline_secret
// is left without a SecretID.
func assertInlineSecretsResolved(spec canonical.Spec) error {
	if spec.MCP == nil {
		return nil
	}
	for _, srv := range spec.MCP.Servers {
		for envKey, value := range srv.Env {
			if value.Mode == canonical.EnvModeInlineSecret && strings.TrimSpace(value.SecretID) == "" {
				return fmt.Errorf("server %q env %q is inline_secret but no encrypted payload was supplied", srv.Name, envKey)
			}
		}
	}
	return nil
}

// validateImportSpecPreCommit is canonical.Spec.Validate() with one tolerance:
// inline_secret entries may have empty SecretID (the tx patches them in).
// Callers MUST re-run Spec.Validate() after the patch.
func validateImportSpecPreCommit(s canonical.Spec) error {
	if s.SchemaVersion <= 0 {
		return fmt.Errorf("schema_version must be > 0")
	}
	switch s.Kind {
	case canonical.KindSkill:
		return s.Validate()
	case canonical.KindMCP:
		if s.MCP == nil {
			return fmt.Errorf("kind=mcp but mcp body is nil")
		}
		if s.Skill != nil || s.Plugin != nil {
			return fmt.Errorf("kind=mcp but another body is set")
		}
		return validateMCPSpecPreCommit(*s.MCP)
	case canonical.KindPlugin:
		// Plugin manifests have no env map: secrets resolve at install time
		// via userConfig on the Claude Code side, not through parsar.
		if s.Plugin == nil {
			return fmt.Errorf("kind=plugin but plugin body is nil")
		}
		if s.MCP != nil || s.Skill != nil {
			return fmt.Errorf("kind=plugin but another body is set")
		}
		return s.Validate()
	case "":
		return fmt.Errorf("missing kind")
	default:
		return fmt.Errorf("unknown kind %q", s.Kind)
	}
}

// validateMCPSpecPreCommit mirrors MCPSpec.Validate but tolerates an empty
// SecretID on inline_secret env entries.
func validateMCPSpecPreCommit(m canonical.MCPSpec) error {
	if len(m.Servers) == 0 {
		return fmt.Errorf("at least one server required")
	}
	seen := make(map[string]struct{}, len(m.Servers))
	for i, srv := range m.Servers {
		if strings.TrimSpace(srv.Name) == "" {
			return fmt.Errorf("server[%d]: name is required", i)
		}
		if strings.TrimSpace(srv.Command) == "" {
			return fmt.Errorf("server %q: command is required", srv.Name)
		}
		if srv.StartupTimeoutSec < 0 {
			return fmt.Errorf("server %q: startup_timeout_sec must be >= 0", srv.Name)
		}
		for name, value := range srv.Env {
			if strings.TrimSpace(name) == "" {
				return fmt.Errorf("server %q: empty env name", srv.Name)
			}
			switch value.Mode {
			case canonical.EnvModeLiteral:
				if value.SecretID != "" || value.CredentialKindCode != "" {
					return fmt.Errorf("server %q env %q: literal mode must not set secret_id/credential_kind_code", srv.Name, name)
				}
			case canonical.EnvModeInlineSecret:
				// Empty SecretID is intentional here — the import tx fills it
				// in. Only reject conflicting fields.
				if value.Literal != "" || value.CredentialKindCode != "" {
					return fmt.Errorf("server %q env %q: inline_secret mode must not set literal/credential_kind_code", srv.Name, name)
				}
			case canonical.EnvModeCredentialRef:
				if strings.TrimSpace(value.CredentialKindCode) == "" {
					return fmt.Errorf("server %q env %q: credential_ref mode requires credential_kind_code", srv.Name, name)
				}
				if value.Literal != "" || value.SecretID != "" {
					return fmt.Errorf("server %q env %q: credential_ref mode must not set literal/secret_id", srv.Name, name)
				}
			default:
				return fmt.Errorf("server %q env %q: unknown env mode %q", srv.Name, name, value.Mode)
			}
		}
		if _, dup := seen[srv.Name]; dup {
			return fmt.Errorf("duplicate server name %q", srv.Name)
		}
		seen[srv.Name] = struct{}{}
	}
	return nil
}

func credentialKindFromListRow(row sqlc.ListCredentialKindsRow) CredentialKindRead {
	return credentialKindFromRow(credentialKindRow(row))
}

func credentialKindFromGetRow(row sqlc.GetCredentialKindByCodeRow) CredentialKindRead {
	return credentialKindFromRow(credentialKindRow(row))
}

func credentialKindFromCreateRow(row sqlc.CreateCredentialKindRow) CredentialKindRead {
	return credentialKindFromRow(credentialKindRow(row))
}

type credentialKindRow sqlc.ListCredentialKindsRow

func credentialKindFromRow(row credentialKindRow) CredentialKindRead {
	return CredentialKindRead{
		ID:          row.ID,
		Code:        row.Code,
		DisplayName: row.DisplayName,
		Description: row.Description,
		ValueSchema: decodeJSONMap(row.ValueSchema),
		BuiltIn:     row.BuiltIn,
		Source:      row.Source,
		CreatedBy:   anyToString(row.CreatedBy),
		CreatedAt:   pgTime(row.CreatedAt),
		UpdatedAt:   pgTime(row.UpdatedAt),
	}
}

// anyToString extracts a string from sqlc-generated `interface{}` columns
// (used for `coalesce(uuid::text, ”)` selects). Empty if missing or wrong type.
func anyToString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case []byte:
		return string(t)
	default:
		return ""
	}
}
