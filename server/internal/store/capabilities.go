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
	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

type RequiredCredential struct {
	Kind        string `json:"kind"`
	Required    bool   `json:"required"`
	Description string `json:"description,omitempty"`
}

type EnabledCapabilityRead struct {
	AgentCapabilityID      string               `json:"agent_capability_id"`
	AgentID                string               `json:"agent_id"`
	Enabled                bool                 `json:"enabled"`
	Configuration          map[string]any       `json:"configuration"`
	// PinningMode is 'latest' or 'pinned'. In 'latest' mode the daemon
	// resolver ignores OssKey/SHA256/CanonicalSpec/Version on this struct
	// and uses LatestOssKey/LatestSHA256/LatestCanonicalSpec/LatestVersion
	// instead — so a re-uploaded capability immediately takes effect on
	// every binding that opted in to 'latest', without rewriting the
	// agent_capabilities.capability_version_id column.
	PinningMode            string               `json:"pinning_mode"`
	CapabilityID           string               `json:"capability_id"`
	WorkspaceID            string               `json:"workspace_id"`
	SourceWorkspaceName    string               `json:"source_workspace_name,omitempty"`
	Type                   string               `json:"type"`
	Name                   string               `json:"name"`
	Description            string               `json:"description"`
	Visibility             string               `json:"visibility"`
	Status                 string               `json:"status"`
	DeprecatedAt           *time.Time           `json:"deprecated_at,omitempty"`
	RequiredCredentials    []RequiredCredential `json:"required_credentials"`
	CapabilityVersionID    string               `json:"capability_version_id"`
	Version                string               `json:"version"`
	LatestVersionID        string               `json:"latest_version_id,omitempty"`
	LatestVersion          string               `json:"latest_version,omitempty"`
	LatestVersionCreatedAt *time.Time           `json:"latest_version_created_at,omitempty"`
	// LatestOssKey / LatestSHA256 / LatestCanonicalSpec / LatestSchemaVersion
	// are the storage breadcrumbs for the capability's CURRENT latest
	// version (resolved at query time via the lateral subquery on
	// capability_version). The daemon resolver reads these when
	// PinningMode == "latest".
	LatestOssKey        string `json:"latest_oss_key,omitempty"`
	LatestSHA256        string `json:"latest_sha256,omitempty"`
	LatestCanonicalSpec []byte `json:"latest_canonical_spec,omitempty"`
	LatestSchemaVersion int16  `json:"latest_schema_version,omitempty"`
	GitRepoURL          string `json:"git_repo_url,omitempty"`
	GitRef              string `json:"git_ref,omitempty"`
	Path                string `json:"path,omitempty"`
	Content             []byte `json:"content,omitempty"`
	// CanonicalSpec carries the scaffold-agnostic capability description
	// (see server/internal/capability/canonical). Empty for legacy rows;
	// the connector falls back to interpreting Content directly. Reflects
	// the pinned version (Pinning="pinned" path).
	CanonicalSpec []byte `json:"canonical_spec,omitempty"`
	// SchemaVersion is the canonical_spec wire-shape version. 0 means
	// "no canonical spec present" (legacy row).
	SchemaVersion int16 `json:"schema_version,omitempty"`
	// OssKey + SHA256 are the authoritative plugin storage breadcrumbs
	// for the PINNED version; the connector reads these columns instead
	// of unpacking canonical_spec jsonb. Empty for mcp / skill rows
	// imported before b77a1c1c.
	OssKey string   `json:"oss_key,omitempty"`
	SHA256 string   `json:"sha256,omitempty"`
	Tags   []string `json:"tags"`
	// CapabilityCreatorID is the original capability creator's user_id.
	CapabilityCreatorID string `json:"capability_creator_id,omitempty"`
}

type CapabilityRead struct {
	ID                     string               `json:"id"`
	WorkspaceID            string               `json:"workspace_id"`
	Type                   string               `json:"type"`
	Name                   string               `json:"name"`
	Description            string               `json:"description"`
	Visibility             string               `json:"visibility"`
	Status                 string               `json:"status"`
	RequiredCredentials    []RequiredCredential `json:"required_credentials"`
	LatestVersionID        string               `json:"latest_version_id,omitempty"`
	LatestVersion          string               `json:"latest_version,omitempty"`
	LatestVersionCreatedAt *time.Time           `json:"latest_version_created_at,omitempty"`
	CreatorID              string               `json:"creator_id"`
	CreatedAt              time.Time            `json:"created_at"`
	UpdatedAt              time.Time            `json:"updated_at"`
	DeletedAt              *time.Time           `json:"deleted_at,omitempty"`
	DeprecatedAt           *time.Time           `json:"deprecated_at,omitempty"`
}

type CapabilityVersionRead struct {
	ID                  string               `json:"id"`
	CapabilityID        string               `json:"capability_id"`
	Version             string               `json:"version"`
	GitRepoURL          string               `json:"git_repo_url,omitempty"`
	GitRef              string               `json:"git_ref,omitempty"`
	Path                string               `json:"path,omitempty"`
	Content             map[string]any       `json:"content,omitempty"`
	SourcePayload       json.RawMessage      `json:"source_payload,omitempty"`
	SchemaVersion       int16                `json:"schema_version"`
	CanonicalSpec       json.RawMessage      `json:"canonical_spec,omitempty"`
	RequiredCredentials []RequiredCredential `json:"required_credentials"`
	// OssKey and SHA256 carry the storage reference for plugin-type
	// capabilities. Empty for mcp/skill rows; the renderer reads them only
	// when capability.type is "plugin".
	OssKey    string    `json:"oss_key,omitempty"`
	SHA256    string    `json:"sha256,omitempty"`
	CreatorID string    `json:"creator_id"`
	CreatedAt time.Time `json:"created_at"`
}

type AgentCapabilityRead struct {
	ID                  string         `json:"id"`
	AgentID             string         `json:"agent_id"`
	CapabilityID        string         `json:"capability_id"`
	CapabilityVersionID string         `json:"capability_version_id"`
	Enabled             bool           `json:"enabled"`
	Configuration       map[string]any `json:"configuration"`
	// PinningMode is 'latest' or 'pinned'. See EnabledCapabilityRead.
	PinningMode string    `json:"pinning_mode"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type MarketplaceCapabilityRead struct {
	CapabilityID           string               `json:"capability_id"`
	SourceWorkspaceID      string               `json:"-"`
	SourceWorkspaceName    string               `json:"source_workspace_name"`
	Type                   string               `json:"type"`
	Name                   string               `json:"name"`
	Description            string               `json:"description"`
	Visibility             string               `json:"visibility"`
	Status                 string               `json:"status"`
	RequiredCredentials    []RequiredCredential `json:"required_credentials"`
	CreatedAt              time.Time            `json:"created_at"`
	UpdatedAt              time.Time            `json:"updated_at"`
	DeprecatedAt           *time.Time           `json:"deprecated_at,omitempty"`
	LatestVersionID        string               `json:"latest_version_id"`
	LatestVersion          string               `json:"latest_version"`
	LatestVersionCreatedAt time.Time            `json:"latest_version_created_at"`
	Installed              bool                 `json:"installed"`
	SelfPublished          bool                 `json:"self_published"`
}

type MarketplaceInstallRead struct {
	CapabilityID           string               `json:"capability_id"`
	Name                   string               `json:"name"`
	Description            string               `json:"description"`
	Type                   string               `json:"type"`
	RequiredCredentials    []RequiredCredential `json:"required_credentials"`
	SourceWorkspaceID      string               `json:"-"`
	SourceWorkspaceName    string               `json:"source_workspace_name"`
	PinnedVersionID        string               `json:"pinned_version_id"`
	PinnedVersion          string               `json:"pinned_version"`
	DeprecatedAt           *time.Time           `json:"deprecated_at,omitempty"`
	LatestVersionID        string               `json:"latest_version_id"`
	LatestPublishedVersion string               `json:"latest_published_version"`
	LatestVersionCreatedAt time.Time            `json:"latest_version_created_at"`
	EnabledAgentCount      int64                `json:"enabled_agent_count"`
	FromMarketplace        bool                 `json:"from_marketplace"`
}

type EnabledMarketplaceAgentRead struct {
	AgentID             string `json:"agent_id"`
	AgentName           string `json:"agent_name"`
	Enabled             bool   `json:"enabled"`
	CapabilityVersionID string `json:"capability_version_id"`
	Version             string `json:"version"`
}

type CreateCapabilityInput struct {
	WorkspaceID    string
	Type           string
	Name           string
	Description    string
	Visibility     string
	CreatorID      string
	InitialVersion *CreateCapabilityVersionInput
}

type UpdateCapabilityInput struct {
	CapabilityID string
	Name         *string
	Description  *string
	Visibility   *string
}

type CreateCapabilityVersionInput struct {
	CapabilityID string
	Version      string
	GitRepoURL   string
	GitRef       string
	Path         string
	Content      map[string]any
	// SourcePayload is the raw user-pasted import body (jsonb shape
	// {"format":"json|toml|markdown","body":"…"}). Optional; nil for
	// versions created outside the import flow.
	SourcePayload json.RawMessage
	// SchemaVersion identifies the canonical_spec schema. Defaults to
	// canonical.SchemaVersionCurrent when 0.
	SchemaVersion int16
	// CanonicalSpec is the cleaned, scaffold-agnostic capability
	// description (server/internal/capability/canonical). Optional;
	// nil for versions that only carry legacy `Content`.
	CanonicalSpec       json.RawMessage
	RequiredCredentials []RequiredCredential
	// OssKey and SHA256 are set ONLY for plugin-type capabilities.
	// OssKey is the object key inside the configured OSS bucket
	// (e.g. "capabilities/plugins/<uuid>/<filename>.zip"); SHA256 is
	// the 64-char lowercase hex digest of the zip body. mcp/skill
	// rows leave both empty.
	OssKey    string
	SHA256    string
	CreatorID string
}

type CreateUserCredentialInput struct {
	UserID         string
	Kind           string
	DisplayName    string
	EncryptedValue []byte
	KeyVersion     string
	Now            time.Time
}

type UpdateUserCredentialInput struct {
	CredentialID   string
	DisplayName    *string
	EncryptedValue []byte
	KeyVersion     string
	Now            time.Time
}

func textValue(v pgtype.Text) string {
	if !v.Valid {
		return ""
	}
	return v.String
}

type UserCredentialRead struct {
	ID          string     `json:"id"`
	UserID      string     `json:"user_id"`
	Kind        string     `json:"kind"`
	DisplayName string     `json:"display_name"`
	Ciphertext  []byte     `json:"-"`
	KeyVersion  string     `json:"key_version"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type ListCapabilityFilter struct {
	Type       string
	Visibility string
	Name       string
	// IncludeDeprecated must be true on the reconciliation path (e.g.
	// syncAgentCapabilities). Otherwise agents still bound to a deprecated
	// capability lose that binding silently on the next save: the lookup
	// built from ListCapabilities wouldn't contain the row, the submitted
	// name wouldn't resolve, and the diff would DELETE it.
	IncludeDeprecated bool
}

func (s *Store) ListCapabilities(ctx context.Context, workspaceID string, filter ListCapabilityFilter) ([]CapabilityRead, error) {
	workspaceUUID, err := uuid(workspaceID)
	if err != nil {
		return nil, err
	}
	rows, err := sqlc.New(s.db).ListCapabilities(ctx, workspaceUUID)
	if err != nil {
		return nil, err
	}
	name := strings.ToLower(strings.TrimSpace(filter.Name))
	out := make([]CapabilityRead, 0, len(rows))
	for _, row := range rows {
		read := capabilityFromListRow(row)
		if filter.Type != "" && read.Type != strings.TrimSpace(filter.Type) {
			continue
		}
		if filter.Visibility != "" && read.Visibility != strings.TrimSpace(filter.Visibility) {
			continue
		}
		if name != "" && !strings.Contains(strings.ToLower(read.Name), name) {
			continue
		}
		if !filter.IncludeDeprecated && read.DeprecatedAt != nil {
			continue
		}
		out = append(out, read)
	}
	return out, nil
}

func (s *Store) ListMarketplaceCapabilities(ctx context.Context, targetWorkspaceID string) ([]MarketplaceCapabilityRead, error) {
	workspaceUUID, err := uuid(targetWorkspaceID)
	if err != nil {
		return nil, err
	}
	rows, err := sqlc.New(s.db).ListMarketplaceCapabilities(ctx, workspaceUUID)
	if err != nil {
		return nil, err
	}
	out := make([]MarketplaceCapabilityRead, 0, len(rows))
	for _, row := range rows {
		out = append(out, marketplaceCapabilityFromRow(row))
	}
	return out, nil
}

func (s *Store) ListWorkspaceMarketplaceInstalls(ctx context.Context, targetWorkspaceID string) ([]MarketplaceInstallRead, error) {
	workspaceUUID, err := uuid(targetWorkspaceID)
	if err != nil {
		return nil, err
	}
	rows, err := sqlc.New(s.db).ListWorkspaceMarketplaceInstalls(ctx, workspaceUUID)
	if err != nil {
		return nil, err
	}
	out := make([]MarketplaceInstallRead, 0, len(rows))
	for _, row := range rows {
		out = append(out, marketplaceInstallFromRow(row))
	}
	return out, nil
}

func (s *Store) CountInstalls(ctx context.Context, sourceCapabilityID string) (int64, error) {
	capabilityUUID, err := uuid(sourceCapabilityID)
	if err != nil {
		return 0, err
	}
	return sqlc.New(s.db).CountInstalls(ctx, capabilityUUID)
}

func (s *Store) ListEnabledAgents(ctx context.Context, targetWorkspaceID string, sourceCapabilityID string) ([]EnabledMarketplaceAgentRead, error) {
	workspaceUUID, err := uuid(targetWorkspaceID)
	if err != nil {
		return nil, err
	}
	capabilityUUID, err := uuid(sourceCapabilityID)
	if err != nil {
		return nil, err
	}
	rows, err := sqlc.New(s.db).ListEnabledAgentsForMarketplaceCapability(ctx, sqlc.ListEnabledAgentsForMarketplaceCapabilityParams{TargetWorkspaceID: workspaceUUID, SourceCapabilityID: capabilityUUID})
	if err != nil {
		return nil, err
	}
	out := make([]EnabledMarketplaceAgentRead, 0, len(rows))
	for _, row := range rows {
		out = append(out, EnabledMarketplaceAgentRead{AgentID: row.AgentID, AgentName: row.AgentName, Enabled: row.Enabled, CapabilityVersionID: row.CapabilityVersionID, Version: row.Version})
	}
	return out, nil
}

func (s *Store) CreateCapability(ctx context.Context, input CreateCapabilityInput) (CapabilityRead, error) {
	now := time.Now().UTC()
	workspaceID, err := uuid(input.WorkspaceID)
	if err != nil {
		return CapabilityRead{}, err
	}
	creatorID, err := uuid(input.CreatorID)
	if err != nil {
		return CapabilityRead{}, err
	}
	row, err := sqlc.New(s.db).CreateCapability(ctx, sqlc.CreateCapabilityParams{
		ID:          mustUUID(newID()),
		WorkspaceID: workspaceID,
		Type:        normalizeCapabilityType(input.Type),
		Name:        strings.TrimSpace(input.Name),
		Description: strings.TrimSpace(input.Description),
		Visibility:  normalizeCapabilityVisibility(input.Visibility),
		// The capability.status column has been retired as a control
		// signal — deprecated_at is the only stop-selling lever now.
		// SQL still requires a non-NULL value (column migration is
		// deferred), so we always write "active" on insert and never
		// expose status as a mutation in the API.
		Status:    "active",
		CreatorID: creatorID,
		Now:       timestamptz(now),
	})
	if err != nil {
		return CapabilityRead{}, err
	}
	capability := capabilityFromCreateRow(row)
	if input.InitialVersion != nil {
		versionInput := *input.InitialVersion
		versionInput.CapabilityID = capability.ID
		versionInput.CreatorID = input.CreatorID
		version, err := s.CreateCapabilityVersion(ctx, versionInput)
		if err != nil {
			return CapabilityRead{}, err
		}
		// required_credentials is version-scoped; surface the initial version's snapshot.
		capability.RequiredCredentials = version.RequiredCredentials
	}
	return capability, nil
}

func (s *Store) GetCapability(ctx context.Context, capabilityID string) (CapabilityRead, error) {
	capabilityUUID, err := uuid(capabilityID)
	if err != nil {
		return CapabilityRead{}, err
	}
	row, err := sqlc.New(s.db).GetCapability(ctx, capabilityUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CapabilityRead{}, fmt.Errorf("%w: %s", ErrUnknownCapability, capabilityID)
		}
		return CapabilityRead{}, err
	}
	read := capabilityFromGetRow(row)
	if read.DeletedAt != nil {
		return CapabilityRead{}, fmt.Errorf("%w: %s", ErrUnknownCapability, capabilityID)
	}
	return read, nil
}

func (s *Store) UpdateCapability(ctx context.Context, input UpdateCapabilityInput) (CapabilityRead, error) {
	existing, err := s.GetCapability(ctx, input.CapabilityID)
	if err != nil {
		return CapabilityRead{}, err
	}
	name := existing.Name
	if input.Name != nil {
		name = strings.TrimSpace(*input.Name)
	}
	description := existing.Description
	if input.Description != nil {
		description = strings.TrimSpace(*input.Description)
	}
	visibility := existing.Visibility
	if input.Visibility != nil {
		visibility = normalizeCapabilityVisibility(*input.Visibility)
	}
	row, err := sqlc.New(s.db).UpdateCapability(ctx, sqlc.UpdateCapabilityParams{
		ID:          mustUUID(input.CapabilityID),
		Name:        name,
		Description: description,
		Visibility:  visibility,
		// Pass through the existing status — the column is no longer a
		// control signal but the SQL UPDATE writes it unconditionally,
		// so preserve whatever the row currently holds (legacy rows may
		// be 'disabled' from before this lever was retired).
		Status: existing.Status,
		Now:    timestamptz(time.Now().UTC()),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CapabilityRead{}, fmt.Errorf("%w: %s", ErrUnknownCapability, input.CapabilityID)
		}
		return CapabilityRead{}, err
	}
	updated := capabilityFromUpdateRow(row)
	// required_credentials is version-scoped; preserve the snapshot since the
	// update query no longer returns it.
	updated.RequiredCredentials = existing.RequiredCredentials
	return updated, nil
}

func (s *Store) PublishCapability(ctx context.Context, workspaceID string, capabilityID string) (CapabilityRead, error) {
	return s.updateCapabilityMarketplaceState(ctx, workspaceID, capabilityID, "public", nil)
}

func (s *Store) UnpublishCapability(ctx context.Context, workspaceID string, capabilityID string) (CapabilityRead, error) {
	return s.updateCapabilityMarketplaceState(ctx, workspaceID, capabilityID, "workspace", nil)
}

func (s *Store) DeprecateCapability(ctx context.Context, workspaceID string, capabilityID string) (CapabilityRead, error) {
	now := time.Now().UTC()
	return s.updateCapabilityMarketplaceState(ctx, workspaceID, capabilityID, "public", &now)
}

func (s *Store) UndeprecateCapability(ctx context.Context, workspaceID string, capabilityID string) (CapabilityRead, error) {
	existing, err := s.GetCapability(ctx, capabilityID)
	if err != nil {
		return CapabilityRead{}, err
	}
	return s.updateCapabilityMarketplaceState(ctx, workspaceID, capabilityID, existing.Visibility, nil)
}

func (s *Store) updateCapabilityMarketplaceState(ctx context.Context, workspaceID string, capabilityID string, visibility string, deprecatedAt *time.Time) (CapabilityRead, error) {
	workspaceUUID, err := uuid(workspaceID)
	if err != nil {
		return CapabilityRead{}, err
	}
	capabilityUUID, err := uuid(capabilityID)
	if err != nil {
		return CapabilityRead{}, err
	}
	deprecated := pgtype.Timestamptz{}
	if deprecatedAt != nil {
		deprecated = timestamptz(*deprecatedAt)
	}
	row, err := sqlc.New(s.db).UpdateCapabilityMarketplaceState(ctx, sqlc.UpdateCapabilityMarketplaceStateParams{ID: capabilityUUID, WorkspaceID: workspaceUUID, Visibility: normalizeCapabilityVisibility(visibility), DeprecatedAt: deprecated, Now: timestamptz(time.Now().UTC())})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CapabilityRead{}, fmt.Errorf("%w: %s", ErrUnknownCapability, capabilityID)
		}
		return CapabilityRead{}, err
	}
	return capabilityFromMarketplaceStateRow(row), nil
}

// SoftDeleteCapability writes capability.deleted_at atomically: the UPDATE carries
// its own `NOT EXISTS (... agent_capabilities ...)` guard, so the caller does not
// need to count first — the "check empty -> someone inserts -> delete" race window
// is closed. When UPDATE returns 0 rows, fetch the count again to distinguish the
// two cases: has references -> CapabilityHasBindingsError; otherwise -> ErrUnknownCapability.
func (s *Store) SoftDeleteCapability(ctx context.Context, workspaceID, capabilityID string) (CapabilityRead, error) {
	capabilityUUID, err := uuid(capabilityID)
	if err != nil {
		return CapabilityRead{}, err
	}
	workspaceUUID, err := uuid(workspaceID)
	if err != nil {
		return CapabilityRead{}, err
	}
	row, err := sqlc.New(s.db).SoftDeleteCapability(ctx, sqlc.SoftDeleteCapabilityParams{
		ID:          capabilityUUID,
		WorkspaceID: workspaceUUID,
		Now:         timestamptz(time.Now().UTC()),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			count, countErr := sqlc.New(s.db).CountAgentBindingsForCapability(ctx, capabilityUUID)
			if countErr == nil && count > 0 {
				return CapabilityRead{}, &CapabilityHasBindingsError{CapabilityID: capabilityID, Count: count}
			}
			return CapabilityRead{}, fmt.Errorf("%w: %s", ErrUnknownCapability, capabilityID)
		}
		return CapabilityRead{}, err
	}
	return capabilityFromSoftDeleteRow(row), nil
}

func (s *Store) ListCapabilityVersions(ctx context.Context, capabilityID string) ([]CapabilityVersionRead, error) {
	capabilityUUID, err := uuid(capabilityID)
	if err != nil {
		return nil, err
	}
	rows, err := sqlc.New(s.db).ListCapabilityVersionsByCapability(ctx, capabilityUUID)
	if err != nil {
		return nil, err
	}
	out := make([]CapabilityVersionRead, 0, len(rows))
	for _, row := range rows {
		out = append(out, capabilityVersionFromListRow(row))
	}
	return out, nil
}

func (s *Store) CreateCapabilityVersion(ctx context.Context, input CreateCapabilityVersionInput) (CapabilityVersionRead, error) {
	content, err := json.Marshal(nonNilMap(input.Content))
	if err != nil {
		return CapabilityVersionRead{}, err
	}
	requiredCredentials, err := s.encodeRegisteredRequiredCredentials(ctx, input.RequiredCredentials)
	if err != nil {
		return CapabilityVersionRead{}, err
	}
	schemaVersion := input.SchemaVersion
	if schemaVersion <= 0 {
		schemaVersion = 1 // capability.canonical.SchemaVersionCurrent — kept literal here to avoid an import cycle
	}
	row, err := sqlc.New(s.db).CreateCapabilityVersion(ctx, sqlc.CreateCapabilityVersionParams{
		ID:                  mustUUID(newID()),
		CapabilityID:        mustUUID(input.CapabilityID),
		Version:             strings.TrimSpace(input.Version),
		GitRepoUrl:          pgNullableText(input.GitRepoURL),
		GitRef:              pgNullableText(input.GitRef),
		Path:                pgNullableText(input.Path),
		Content:             content,
		SourcePayload:       []byte(input.SourcePayload),
		SchemaVersion:       schemaVersion,
		CanonicalSpec:       []byte(input.CanonicalSpec),
		RequiredCredentials: requiredCredentials,
		OssKey:              strings.TrimSpace(input.OssKey),
		Sha256:              strings.ToLower(strings.TrimSpace(input.SHA256)),
		CreatorID:           mustUUID(input.CreatorID),
		Now:                 timestamptz(time.Now().UTC()),
	})
	if err != nil {
		return CapabilityVersionRead{}, err
	}
	return capabilityVersionFromCreateRow(row), nil
}

func (s *Store) GetCapabilityVersion(ctx context.Context, capabilityVersionID string) (CapabilityVersionRead, error) {
	row, err := sqlc.New(s.db).GetCapabilityVersion(ctx, mustUUID(capabilityVersionID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CapabilityVersionRead{}, fmt.Errorf("%w: %s", ErrUnknownCapabilityVersion, capabilityVersionID)
		}
		return CapabilityVersionRead{}, err
	}
	return capabilityVersionFromGetRow(row), nil
}

func (s *Store) UpdateCapabilityVersionContent(ctx context.Context, capabilityVersionID string, _ map[string]any) (CapabilityVersionRead, error) {
	return CapabilityVersionRead{}, fmt.Errorf("%w: %s", ErrImmutable, capabilityVersionID)
}

func (s *Store) DeleteCapabilityVersion(ctx context.Context, capabilityVersionID string) error {
	return fmt.Errorf("%w: %s", ErrImmutable, capabilityVersionID)
}

func (s *Store) ListUserCredentials(ctx context.Context, userID string) ([]UserCredentialRead, error) {
	rows, err := sqlc.New(s.db).ListUserCredentialsByUser(ctx, mustUUID(userID))
	if err != nil {
		return nil, err
	}
	out := make([]UserCredentialRead, 0, len(rows))
	for _, row := range rows {
		out = append(out, userCredentialFromListRow(row))
	}
	return out, nil
}

func (s *Store) CreateUserCredential(ctx context.Context, input CreateUserCredentialInput) (UserCredentialRead, error) {
	now := input.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	kind, err := s.normalizeRegisteredCredentialKind(ctx, input.Kind)
	if err != nil {
		return UserCredentialRead{}, err
	}
	keyVersion := strings.TrimSpace(input.KeyVersion)
	if keyVersion == "" {
		keyVersion = "v1"
	}
	row, err := sqlc.New(s.db).CreateUserCredential(ctx, sqlc.CreateUserCredentialParams{
		ID:          mustUUID(newID()),
		UserID:      mustUUID(input.UserID),
		Kind:        kind,
		DisplayName: strings.TrimSpace(input.DisplayName),
		Ciphertext:  input.EncryptedValue,
		KeyVersion:  keyVersion,
		Now:         timestamptz(now),
	})
	if err != nil {
		return UserCredentialRead{}, err
	}
	return UserCredentialReadFromCreateRow(row), nil
}

// ReplaceUserCredentialResult is one slot's outcome from ReplaceUserCredentials.
// Replaced=true means an existing active row was soft-deleted to make room for
// the fresh INSERT — surfaced so credential overwrites aren't silent
// (user_credentials enforces one active row per (user_id, kind)).
type ReplaceUserCredentialResult struct {
	Kind       string
	Credential UserCredentialRead
	Replaced   bool
}

// ReplaceUserCredentials atomically replaces N user_credentials rows for one
// user in a single tx: per slot it soft-deletes any existing active row of the
// same kind then INSERTs the fresh ciphertext. All commit together or all
// roll back. Returns per-kind Replaced markers.
//
// CreateUserCredential is INSERT-only and crashes against the
// `(user_id, kind) WHERE deleted_at IS NULL` partial unique index when the
// user re-binds a rotated token, and a multi-slot loop using it leaked partial
// writes on the Nth-slot failure.
func (s *Store) ReplaceUserCredentials(ctx context.Context, userID string, inputs []CreateUserCredentialInput) ([]ReplaceUserCredentialResult, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, fmt.Errorf("user_id is required")
	}
	if len(inputs) == 0 {
		return nil, nil
	}
	userUUID, err := uuid(userID)
	if err != nil {
		return nil, fmt.Errorf("user_id: %w", err)
	}

	// Resolve every kind against credential_kinds outside the tx so a typo
	// doesn't roll back a half-built batch.
	resolved := make([]struct {
		kind  string
		input CreateUserCredentialInput
	}, 0, len(inputs))
	for _, in := range inputs {
		kind, err := s.normalizeRegisteredCredentialKind(ctx, in.Kind)
		if err != nil {
			return nil, err
		}
		resolved = append(resolved, struct {
			kind  string
			input CreateUserCredentialInput
		}{kind: kind, input: in})
	}

	tx, err := beginTx(ctx, s.db)
	if err != nil {
		return nil, fmt.Errorf("replace user credentials: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New(tx)

	now := time.Now().UTC()
	out := make([]ReplaceUserCredentialResult, 0, len(resolved))
	for _, r := range resolved {
		rowNow := r.input.Now
		if rowNow.IsZero() {
			rowNow = now
		}
		keyVersion := strings.TrimSpace(r.input.KeyVersion)
		if keyVersion == "" {
			keyVersion = "v1"
		}
		// Soft-delete the existing active row (if any). The affected count
		// distinguishes "fresh write" from "replaced an existing credential".
		replaced, err := q.SoftDeleteUserCredentialByUserKind(ctx, sqlc.SoftDeleteUserCredentialByUserKindParams{
			UserID: userUUID,
			Kind:   r.kind,
			Now:    timestamptz(rowNow),
		})
		if err != nil {
			return nil, fmt.Errorf("replace user credentials: soft-delete %s: %w", r.kind, err)
		}
		row, err := q.CreateUserCredential(ctx, sqlc.CreateUserCredentialParams{
			ID:          mustUUID(newID()),
			UserID:      userUUID,
			Kind:        r.kind,
			DisplayName: strings.TrimSpace(r.input.DisplayName),
			Ciphertext:  r.input.EncryptedValue,
			KeyVersion:  keyVersion,
			Now:         timestamptz(rowNow),
		})
		if err != nil {
			return nil, fmt.Errorf("replace user credentials: insert %s: %w", r.kind, err)
		}
		out = append(out, ReplaceUserCredentialResult{
			Kind:       r.kind,
			Credential: UserCredentialReadFromCreateRow(row),
			Replaced:   replaced > 0,
		})
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("replace user credentials: commit: %w", err)
	}
	return out, nil
}

func (s *Store) GetUserCredential(ctx context.Context, credentialID string) (UserCredentialRead, error) {
	row, err := sqlc.New(s.db).GetUserCredential(ctx, mustUUID(credentialID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return UserCredentialRead{}, fmt.Errorf("%w: %s", ErrUnknownUserCredential, credentialID)
		}
		return UserCredentialRead{}, err
	}
	read := userCredentialFromGetRow(row)
	if row.DeletedAt.Valid {
		return UserCredentialRead{}, fmt.Errorf("%w: %s", ErrUnknownUserCredential, credentialID)
	}
	return read, nil
}

func (s *Store) UpdateUserCredential(ctx context.Context, input UpdateUserCredentialInput) (UserCredentialRead, error) {
	existing, err := s.GetUserCredential(ctx, input.CredentialID)
	if err != nil {
		return UserCredentialRead{}, err
	}
	displayName := existing.DisplayName
	if input.DisplayName != nil {
		displayName = strings.TrimSpace(*input.DisplayName)
	}
	ciphertext := input.EncryptedValue
	keyVersion := strings.TrimSpace(input.KeyVersion)
	if len(ciphertext) == 0 {
		ciphertext = existing.Ciphertext
		keyVersion = existing.KeyVersion
	}
	if keyVersion == "" {
		keyVersion = "v1"
	}
	now := input.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	row, err := sqlc.New(s.db).UpdateUserCredential(ctx, sqlc.UpdateUserCredentialParams{
		ID:          mustUUID(input.CredentialID),
		DisplayName: displayName,
		Ciphertext:  ciphertext,
		KeyVersion:  keyVersion,
		Now:         timestamptz(now),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return UserCredentialRead{}, fmt.Errorf("%w: %s", ErrUnknownUserCredential, input.CredentialID)
		}
		return UserCredentialRead{}, err
	}
	return userCredentialFromUpdateRow(row), nil
}

func (s *Store) SoftDeleteUserCredential(ctx context.Context, credentialID string) (UserCredentialRead, error) {
	row, err := sqlc.New(s.db).SoftDeleteUserCredential(ctx, sqlc.SoftDeleteUserCredentialParams{ID: mustUUID(credentialID), Now: timestamptz(time.Now().UTC())})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return UserCredentialRead{}, fmt.Errorf("%w: %s", ErrUnknownUserCredential, credentialID)
		}
		return UserCredentialRead{}, err
	}
	return userCredentialFromDeleteRow(row), nil
}

func (s *Store) ListAgentCapabilities(ctx context.Context, agentID string) ([]AgentCapabilityRead, error) {
	rows, err := sqlc.New(s.db).ListAgentCapabilitiesByAgent(ctx, mustUUID(agentID))
	if err != nil {
		return nil, err
	}
	out := make([]AgentCapabilityRead, 0, len(rows))
	for _, row := range rows {
		out = append(out, agentCapabilityFromListRow(row))
	}
	return out, nil
}

// PinningModeLatest / PinningModePinned name the two valid values of
// agent_capabilities.pinning_mode. Callers should pass these constants
// instead of bare strings; normalizePinningMode below also recognises
// the empty string as "use the safer pinned default".
const (
	PinningModeLatest = "latest"
	PinningModePinned = "pinned"
)

// normalizePinningMode falls back to PinningModePinned when the caller
// passes "" — pinned matches the column's DB default and is the safer
// behaviour when a caller forgot to plumb the mode through. Unknown
// non-empty values are returned untouched so the CHECK constraint can
// reject them loudly instead of being silently rewritten.
func normalizePinningMode(mode string) string {
	switch strings.TrimSpace(mode) {
	case "":
		return PinningModePinned
	default:
		return strings.TrimSpace(mode)
	}
}

func (s *Store) EnableAgentCapability(ctx context.Context, agentID string, versionID string, configuration map[string]any, pinningMode string) (AgentCapabilityRead, error) {
	version, err := s.GetCapabilityVersion(ctx, versionID)
	if err != nil {
		return AgentCapabilityRead{}, err
	}
	config, err := json.Marshal(nonNilMap(configuration))
	if err != nil {
		return AgentCapabilityRead{}, err
	}
	mode := normalizePinningMode(pinningMode)
	now := time.Now().UTC()
	params := sqlc.CreateAgentCapabilityParams{ID: mustUUID(newID()), AgentID: mustUUID(agentID), CapabilityID: mustUUID(version.CapabilityID), CapabilityVersionID: mustUUID(version.ID), Enabled: true, Configuration: config, PinningMode: mode, Now: timestamptz(now)}
	row, err := sqlc.New(s.db).CreateAgentCapability(ctx, params)
	if err == nil {
		return agentCapabilityFromCreateRow(row), nil
	}
	if !isUniqueViolation(err) {
		return AgentCapabilityRead{}, err
	}
	existingRows, err := sqlc.New(s.db).ListAgentCapabilitiesByAgent(ctx, mustUUID(agentID))
	if err != nil {
		return AgentCapabilityRead{}, err
	}
	for _, existing := range existingRows {
		if existing.CapabilityID == version.CapabilityID {
			updated, err := sqlc.New(s.db).UpdateAgentCapability(ctx, sqlc.UpdateAgentCapabilityParams{ID: mustUUID(existing.ID), CapabilityVersionID: mustUUID(version.ID), Enabled: true, Configuration: config, PinningMode: mode, Now: timestamptz(now)})
			if err != nil {
				return AgentCapabilityRead{}, err
			}
			return agentCapabilityFromUpdateRow(updated), nil
		}
	}
	return AgentCapabilityRead{}, err
}

func (s *Store) UpgradeAgentCapability(ctx context.Context, agentID string, capabilityID string, newVersionID string, pinningMode string) (AgentCapabilityRead, error) {
	mode := normalizePinningMode(pinningMode)
	row, err := sqlc.New(s.db).UpgradeAgentCapability(ctx, sqlc.UpgradeAgentCapabilityParams{AgentID: mustUUID(agentID), CapabilityID: mustUUID(capabilityID), NewVersionID: mustUUID(newVersionID), PinningMode: mode, Now: timestamptz(time.Now().UTC())})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AgentCapabilityRead{}, fmt.Errorf("%w: %s", ErrMarketplaceCapabilityUnavailable, capabilityID)
		}
		return AgentCapabilityRead{}, err
	}
	return agentCapabilityFromUpgradeRow(row), nil
}

func (s *Store) UninstallWorkspaceMarketplaceCapability(ctx context.Context, targetWorkspaceID string, sourceCapabilityID string) (int64, error) {
	targetUUID, err := uuid(targetWorkspaceID)
	if err != nil {
		return 0, err
	}
	capability, err := s.GetCapability(ctx, sourceCapabilityID)
	if err != nil {
		return 0, err
	}
	if capability.WorkspaceID == targetWorkspaceID {
		return 0, fmt.Errorf("%w: source capability must belong to another workspace", ErrMarketplaceCapabilityUnavailable)
	}
	sourceUUID, err := uuid(sourceCapabilityID)
	if err != nil {
		return 0, err
	}
	return sqlc.New(s.db).UninstallWorkspaceMarketplaceCapability(ctx, sqlc.UninstallWorkspaceMarketplaceCapabilityParams{TargetWorkspaceID: targetUUID, SourceCapabilityID: sourceUUID})
}

func (s *Store) DeleteAgentCapability(ctx context.Context, agentID string, capabilityVersionID string) error {
	rows, err := sqlc.New(s.db).ListAgentCapabilitiesByAgent(ctx, mustUUID(agentID))
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.CapabilityVersionID == capabilityVersionID || row.CapabilityID == capabilityVersionID {
			return sqlc.New(s.db).DeleteAgentCapability(ctx, mustUUID(row.ID))
		}
	}
	return fmt.Errorf("%w: %s", ErrUnknownAgentCapability, capabilityVersionID)
}

// IsBuiltinCapabilityEnabled reports whether a runtime-injected built-in tool
// (e.g. fetch_chat_history) is enabled for the agent. Built-ins default to ON:
// no row means enabled, so pgx.ErrNoRows resolves to true.
func (s *Store) IsBuiltinCapabilityEnabled(ctx context.Context, agentID, key string) (bool, error) {
	enabled, err := sqlc.New(s.db).GetBuiltinCapabilityEnabled(ctx, sqlc.GetBuiltinCapabilityEnabledParams{
		AgentID:       mustUUID(agentID),
		CapabilityKey: key,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return true, nil
		}
		return false, err
	}
	return enabled, nil
}

// SetBuiltinCapabilityEnabled upserts the per-agent on/off flag for a built-in
// tool. Writing enabled=true is a no-op relative to the default but records an
// explicit row; enabled=false is what disables the tool for this agent.
func (s *Store) SetBuiltinCapabilityEnabled(ctx context.Context, agentID, key string, enabled bool) error {
	return sqlc.New(s.db).SetBuiltinCapabilityEnabled(ctx, sqlc.SetBuiltinCapabilityEnabledParams{
		AgentID:       mustUUID(agentID),
		CapabilityKey: key,
		Enabled:       enabled,
	})
}

func pgNullableText(value string) pgtype.Text {
	value = strings.TrimSpace(value)
	if value == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: value, Valid: true}
}

// normalizeCapabilityType is the type-column gatekeeper for the
// capability table. Unknown types silently fall back to "mcp" so the
// INSERT still satisfies the NOT NULL constraint, but the silent
// rewrite has bitten us before — "system_prompt" was missing here and
// every system_prompt capability landed in the MCP tab with its real
// kind hidden inside canonical_spec. Any new Kind added to
// canonical.Kind MUST be added to this allowlist as well.
func normalizeCapabilityType(value string) string {
	switch strings.TrimSpace(value) {
	case "skill", "mcp", "plugin", "system_prompt":
		return strings.TrimSpace(value)
	default:
		return "mcp"
	}
}

func normalizeCapabilityVisibility(value string) string {
	switch strings.TrimSpace(value) {
	case "public", "workspace":
		return strings.TrimSpace(value)
	default:
		return "workspace"
	}
}

func capabilityFromCreateRow(row sqlc.CreateCapabilityRow) CapabilityRead {
	return CapabilityRead{ID: row.ID, WorkspaceID: row.WorkspaceID, Type: row.Type, Name: row.Name, Description: row.Description, Visibility: row.Visibility, Status: row.Status, RequiredCredentials: []RequiredCredential{}, CreatorID: row.CreatorID, CreatedAt: pgTime(row.CreatedAt), UpdatedAt: pgTime(row.UpdatedAt), DeletedAt: pgOptionalTime(row.DeletedAt), DeprecatedAt: pgOptionalTime(row.DeprecatedAt)}
}

func capabilityFromGetRow(row sqlc.GetCapabilityRow) CapabilityRead {
	latestCreatedAt := pgOptionalTime(row.LatestVersionCreatedAt)
	return CapabilityRead{ID: row.ID, WorkspaceID: row.WorkspaceID, Type: row.Type, Name: row.Name, Description: row.Description, Visibility: row.Visibility, Status: row.Status, RequiredCredentials: decodeRequiredCredentials(row.RequiredCredentials), LatestVersionID: row.LatestVersionID, LatestVersion: row.LatestVersion, LatestVersionCreatedAt: latestCreatedAt, CreatorID: row.CreatorID, CreatedAt: pgTime(row.CreatedAt), UpdatedAt: pgTime(row.UpdatedAt), DeletedAt: pgOptionalTime(row.DeletedAt), DeprecatedAt: pgOptionalTime(row.DeprecatedAt)}
}

func capabilityFromListRow(row sqlc.ListCapabilitiesRow) CapabilityRead {
	latestCreatedAt := pgOptionalTime(row.LatestVersionCreatedAt)
	return CapabilityRead{ID: row.ID, WorkspaceID: row.WorkspaceID, Type: row.Type, Name: row.Name, Description: row.Description, Visibility: row.Visibility, Status: row.Status, RequiredCredentials: decodeRequiredCredentials(row.RequiredCredentials), LatestVersionID: row.LatestVersionID, LatestVersion: row.LatestVersion, LatestVersionCreatedAt: latestCreatedAt, CreatorID: row.CreatorID, CreatedAt: pgTime(row.CreatedAt), UpdatedAt: pgTime(row.UpdatedAt), DeletedAt: pgOptionalTime(row.DeletedAt), DeprecatedAt: pgOptionalTime(row.DeprecatedAt)}
}

func capabilityFromUpdateRow(row sqlc.UpdateCapabilityRow) CapabilityRead {
	return CapabilityRead{ID: row.ID, WorkspaceID: row.WorkspaceID, Type: row.Type, Name: row.Name, Description: row.Description, Visibility: row.Visibility, Status: row.Status, RequiredCredentials: []RequiredCredential{}, CreatorID: row.CreatorID, CreatedAt: pgTime(row.CreatedAt), UpdatedAt: pgTime(row.UpdatedAt), DeletedAt: pgOptionalTime(row.DeletedAt), DeprecatedAt: pgOptionalTime(row.DeprecatedAt)}
}

func capabilityFromSoftDeleteRow(row sqlc.SoftDeleteCapabilityRow) CapabilityRead {
	return CapabilityRead{ID: row.ID, WorkspaceID: row.WorkspaceID, Type: row.Type, Name: row.Name, Description: row.Description, Visibility: row.Visibility, Status: row.Status, RequiredCredentials: []RequiredCredential{}, CreatorID: row.CreatorID, CreatedAt: pgTime(row.CreatedAt), UpdatedAt: pgTime(row.UpdatedAt), DeletedAt: pgOptionalTime(row.DeletedAt), DeprecatedAt: pgOptionalTime(row.DeprecatedAt)}
}

func capabilityFromMarketplaceStateRow(row sqlc.UpdateCapabilityMarketplaceStateRow) CapabilityRead {
	return CapabilityRead{ID: row.ID, WorkspaceID: row.WorkspaceID, Type: row.Type, Name: row.Name, Description: row.Description, Visibility: row.Visibility, Status: row.Status, RequiredCredentials: []RequiredCredential{}, CreatorID: row.CreatorID, CreatedAt: pgTime(row.CreatedAt), UpdatedAt: pgTime(row.UpdatedAt), DeletedAt: pgOptionalTime(row.DeletedAt), DeprecatedAt: pgOptionalTime(row.DeprecatedAt)}
}

func marketplaceCapabilityFromRow(row sqlc.ListMarketplaceCapabilitiesRow) MarketplaceCapabilityRead {
	return MarketplaceCapabilityRead{CapabilityID: row.CapabilityID, SourceWorkspaceID: row.SourceWorkspaceID, SourceWorkspaceName: row.SourceWorkspaceName, Type: row.Type, Name: row.Name, Description: row.Description, Visibility: row.Visibility, Status: row.Status, RequiredCredentials: decodeRequiredCredentials(row.RequiredCredentials), CreatedAt: pgTime(row.CreatedAt), UpdatedAt: pgTime(row.UpdatedAt), DeprecatedAt: pgOptionalTime(row.DeprecatedAt), LatestVersionID: row.LatestVersionID, LatestVersion: row.LatestVersion, LatestVersionCreatedAt: pgTime(row.LatestVersionCreatedAt), Installed: row.Installed, SelfPublished: row.SelfPublished}
}

func marketplaceInstallFromRow(row sqlc.ListWorkspaceMarketplaceInstallsRow) MarketplaceInstallRead {
	return MarketplaceInstallRead{CapabilityID: row.CapabilityID, Name: row.Name, Description: row.Description, Type: row.Type, RequiredCredentials: decodeRequiredCredentials(row.RequiredCredentials), SourceWorkspaceID: row.SourceWorkspaceID, SourceWorkspaceName: row.SourceWorkspaceName, PinnedVersionID: row.PinnedVersionID, PinnedVersion: row.PinnedVersion, DeprecatedAt: pgOptionalTime(row.DeprecatedAt), LatestVersionID: row.LatestVersionID, LatestPublishedVersion: row.LatestPublishedVersion, LatestVersionCreatedAt: pgTime(row.LatestVersionCreatedAt), EnabledAgentCount: row.EnabledAgentCount, FromMarketplace: true}
}

func decodeRequiredCredentials(raw []byte) []RequiredCredential {
	if len(raw) == 0 {
		return []RequiredCredential{}
	}
	var out []RequiredCredential
	if err := json.Unmarshal(raw, &out); err != nil || out == nil {
		return []RequiredCredential{}
	}
	return out
}

func encodeRequiredCredentials(creds []RequiredCredential) ([]byte, error) {
	normalized, err := normalizeRequiredCredentials(creds)
	if err != nil {
		return nil, err
	}
	return json.Marshal(normalized)
}

func (s *Store) encodeRegisteredRequiredCredentials(ctx context.Context, creds []RequiredCredential) ([]byte, error) {
	normalized, err := s.normalizeRegisteredRequiredCredentials(ctx, creds)
	if err != nil {
		return nil, err
	}
	return json.Marshal(normalized)
}

func capabilityVersionContent(raw []byte) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	decoded := map[string]any{}
	if err := json.Unmarshal(raw, &decoded); err != nil || len(decoded) == 0 {
		return nil
	}
	return decoded
}

func capabilityVersionFromCreateRow(row sqlc.CreateCapabilityVersionRow) CapabilityVersionRead {
	return CapabilityVersionRead{ID: row.ID, CapabilityID: row.CapabilityID, Version: row.Version, GitRepoURL: textValue(row.GitRepoUrl), GitRef: textValue(row.GitRef), Path: textValue(row.Path), Content: capabilityVersionContent(row.Content), SourcePayload: copyRawJSON(row.SourcePayload), SchemaVersion: row.SchemaVersion, CanonicalSpec: copyRawJSON(row.CanonicalSpec), RequiredCredentials: decodeRequiredCredentials(row.RequiredCredentials), OssKey: row.OssKey, SHA256: row.Sha256, CreatorID: row.CreatorID, CreatedAt: pgTime(row.CreatedAt)}
}

func capabilityVersionFromGetRow(row sqlc.GetCapabilityVersionRow) CapabilityVersionRead {
	return CapabilityVersionRead{ID: row.ID, CapabilityID: row.CapabilityID, Version: row.Version, GitRepoURL: textValue(row.GitRepoUrl), GitRef: textValue(row.GitRef), Path: textValue(row.Path), Content: capabilityVersionContent(row.Content), SourcePayload: copyRawJSON(row.SourcePayload), SchemaVersion: row.SchemaVersion, CanonicalSpec: copyRawJSON(row.CanonicalSpec), RequiredCredentials: decodeRequiredCredentials(row.RequiredCredentials), OssKey: row.OssKey, SHA256: row.Sha256, CreatorID: row.CreatorID, CreatedAt: pgTime(row.CreatedAt)}
}

func capabilityVersionFromListRow(row sqlc.ListCapabilityVersionsByCapabilityRow) CapabilityVersionRead {
	return CapabilityVersionRead{ID: row.ID, CapabilityID: row.CapabilityID, Version: row.Version, GitRepoURL: textValue(row.GitRepoUrl), GitRef: textValue(row.GitRef), Path: textValue(row.Path), Content: capabilityVersionContent(row.Content), SourcePayload: copyRawJSON(row.SourcePayload), SchemaVersion: row.SchemaVersion, CanonicalSpec: copyRawJSON(row.CanonicalSpec), RequiredCredentials: decodeRequiredCredentials(row.RequiredCredentials), OssKey: row.OssKey, SHA256: row.Sha256, CreatorID: row.CreatorID, CreatedAt: pgTime(row.CreatedAt)}
}

// copyRawJSON returns a defensive copy of the underlying jsonb bytes so the
// returned RawMessage does not alias the sqlc-owned buffer (which gets reused
// on the next Scan).
func copyRawJSON(raw []byte) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	out := make(json.RawMessage, len(raw))
	copy(out, raw)
	return out
}

func userCredentialFromGetRow(row sqlc.GetUserCredentialRow) UserCredentialRead {
	return UserCredentialRead{ID: row.ID, UserID: row.UserID, Kind: row.Kind, DisplayName: row.DisplayName, Ciphertext: append([]byte(nil), row.Ciphertext...), KeyVersion: row.KeyVersion, LastUsedAt: pgOptionalTime(row.LastUsedAt), CreatedAt: pgTime(row.CreatedAt), UpdatedAt: pgTime(row.UpdatedAt)}
}

func userCredentialFromListRow(row sqlc.ListUserCredentialsByUserRow) UserCredentialRead {
	return UserCredentialRead{ID: row.ID, UserID: row.UserID, Kind: row.Kind, DisplayName: row.DisplayName, Ciphertext: append([]byte(nil), row.Ciphertext...), KeyVersion: row.KeyVersion, LastUsedAt: pgOptionalTime(row.LastUsedAt), CreatedAt: pgTime(row.CreatedAt), UpdatedAt: pgTime(row.UpdatedAt)}
}

func userCredentialFromUpdateRow(row sqlc.UpdateUserCredentialRow) UserCredentialRead {
	return UserCredentialRead{ID: row.ID, UserID: row.UserID, Kind: row.Kind, DisplayName: row.DisplayName, Ciphertext: append([]byte(nil), row.Ciphertext...), KeyVersion: row.KeyVersion, LastUsedAt: pgOptionalTime(row.LastUsedAt), CreatedAt: pgTime(row.CreatedAt), UpdatedAt: pgTime(row.UpdatedAt)}
}

func userCredentialFromDeleteRow(row sqlc.SoftDeleteUserCredentialRow) UserCredentialRead {
	return UserCredentialRead{ID: row.ID, UserID: row.UserID, Kind: row.Kind, DisplayName: row.DisplayName, Ciphertext: append([]byte(nil), row.Ciphertext...), KeyVersion: row.KeyVersion, LastUsedAt: pgOptionalTime(row.LastUsedAt), CreatedAt: pgTime(row.CreatedAt), UpdatedAt: pgTime(row.UpdatedAt)}
}

func agentCapabilityFromCreateRow(row sqlc.CreateAgentCapabilityRow) AgentCapabilityRead {
	return AgentCapabilityRead{ID: row.ID, AgentID: row.AgentID, CapabilityID: row.CapabilityID, CapabilityVersionID: row.CapabilityVersionID, Enabled: row.Enabled, Configuration: decodeJSONMap(row.Configuration), PinningMode: row.PinningMode, CreatedAt: pgTime(row.CreatedAt), UpdatedAt: pgTime(row.UpdatedAt)}
}

func agentCapabilityFromListRow(row sqlc.ListAgentCapabilitiesByAgentRow) AgentCapabilityRead {
	return AgentCapabilityRead{ID: row.ID, AgentID: row.AgentID, CapabilityID: row.CapabilityID, CapabilityVersionID: row.CapabilityVersionID, Enabled: row.Enabled, Configuration: decodeJSONMap(row.Configuration), PinningMode: row.PinningMode, CreatedAt: pgTime(row.CreatedAt), UpdatedAt: pgTime(row.UpdatedAt)}
}

func agentCapabilityFromUpdateRow(row sqlc.UpdateAgentCapabilityRow) AgentCapabilityRead {
	return AgentCapabilityRead{ID: row.ID, AgentID: row.AgentID, CapabilityID: row.CapabilityID, CapabilityVersionID: row.CapabilityVersionID, Enabled: row.Enabled, Configuration: decodeJSONMap(row.Configuration), PinningMode: row.PinningMode, CreatedAt: pgTime(row.CreatedAt), UpdatedAt: pgTime(row.UpdatedAt)}
}

func agentCapabilityFromUpgradeRow(row sqlc.UpgradeAgentCapabilityRow) AgentCapabilityRead {
	return AgentCapabilityRead{ID: row.ID, AgentID: row.AgentID, CapabilityID: row.CapabilityID, CapabilityVersionID: row.CapabilityVersionID, Enabled: row.Enabled, Configuration: decodeJSONMap(row.Configuration), PinningMode: row.PinningMode, CreatedAt: pgTime(row.CreatedAt), UpdatedAt: pgTime(row.UpdatedAt)}
}

func (s *Store) GetEnabledCapabilitiesForAgent(ctx context.Context, agentID string) ([]EnabledCapabilityRead, error) {
	agentUUID, err := uuid(agentID)
	if err != nil {
		return nil, err
	}
	rows, err := sqlc.New(s.db).GetEnabledCapabilitiesForAgent(ctx, agentUUID)
	if err != nil {
		return nil, err
	}
	out := make([]EnabledCapabilityRead, 0, len(rows))
	for _, row := range rows {
		capability, err := enabledCapabilityFromRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, capability)
	}
	return out, nil
}

func (s *Store) GetEnabledMarketplaceCapabilitiesForAgent(ctx context.Context, agentID string) ([]EnabledCapabilityRead, error) {
	return s.GetEnabledCapabilitiesForAgent(ctx, agentID)
}

func (s *Store) GetUserCredentialByUserKind(ctx context.Context, userID, kind string) (UserCredentialRead, bool, error) {
	userUUID, err := uuid(userID)
	if err != nil {
		return UserCredentialRead{}, false, err
	}
	row, err := sqlc.New(s.db).GetUserCredentialByUserKind(ctx, sqlc.GetUserCredentialByUserKindParams{
		UserID: userUUID,
		Kind:   strings.TrimSpace(kind),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return UserCredentialRead{}, false, nil
		}
		return UserCredentialRead{}, false, err
	}
	return userCredentialFromRow(row), true, nil
}

func enabledCapabilityFromRow(row sqlc.GetEnabledCapabilitiesForAgentRow) (EnabledCapabilityRead, error) {
	configuration := decodeJSONMap(row.Configuration)
	tags, err := decodeStringTags(row.Tags)
	if err != nil {
		return EnabledCapabilityRead{}, fmt.Errorf("capability %s: decode tags: %w", row.CapabilityID, err)
	}
	return EnabledCapabilityRead{
		AgentCapabilityID:      row.AgentCapabilityID,
		AgentID:                row.AgentID,
		Enabled:                row.Enabled,
		Configuration:          configuration,
		PinningMode:            row.PinningMode,
		CapabilityID:           row.CapabilityID,
		WorkspaceID:            row.WorkspaceID,
		SourceWorkspaceName:    row.SourceWorkspaceName,
		Type:                   row.Type,
		Name:                   row.Name,
		Description:            row.Description,
		Visibility:             row.Visibility,
		Status:                 row.Status,
		DeprecatedAt:           pgOptionalTime(row.DeprecatedAt),
		RequiredCredentials:    decodeRequiredCredentials(row.RequiredCredentials),
		CapabilityVersionID:    row.CapabilityVersionID,
		Version:                row.Version,
		LatestVersionID:        row.LatestVersionID,
		LatestVersion:          row.LatestVersion,
		LatestVersionCreatedAt: pgOptionalTime(row.LatestVersionCreatedAt),
		LatestOssKey:           row.LatestOssKey,
		LatestSHA256:           row.LatestSha256,
		LatestCanonicalSpec:    append([]byte(nil), row.LatestCanonicalSpec...),
		LatestSchemaVersion:    row.LatestSchemaVersion,
		GitRepoURL:             textValue(row.GitRepoUrl),
		GitRef:                 textValue(row.GitRef),
		Path:                   textValue(row.Path),
		Content:                append([]byte(nil), row.Content...),
		CanonicalSpec:          append([]byte(nil), row.CanonicalSpec...),
		SchemaVersion:          row.SchemaVersion,
		OssKey:                 row.OssKey,
		SHA256:                 row.Sha256,
		Tags:                   tags,
		CapabilityCreatorID:    row.CapabilityCreatorID,
	}, nil
}

func userCredentialFromRow(row sqlc.GetUserCredentialByUserKindRow) UserCredentialRead {
	return UserCredentialRead{
		ID:          row.ID,
		UserID:      row.UserID,
		Kind:        row.Kind,
		DisplayName: row.DisplayName,
		Ciphertext:  append([]byte(nil), row.Ciphertext...),
		KeyVersion:  row.KeyVersion,
		LastUsedAt:  pgOptionalTime(row.LastUsedAt),
		CreatedAt:   pgTime(row.CreatedAt),
		UpdatedAt:   pgTime(row.UpdatedAt),
	}
}

func UserCredentialReadFromCreateRow(row sqlc.CreateUserCredentialRow) UserCredentialRead {
	return UserCredentialRead{
		ID:          row.ID,
		UserID:      row.UserID,
		Kind:        row.Kind,
		DisplayName: row.DisplayName,
		Ciphertext:  append([]byte(nil), row.Ciphertext...),
		KeyVersion:  row.KeyVersion,
		LastUsedAt:  pgOptionalTime(row.LastUsedAt),
		CreatedAt:   pgTime(row.CreatedAt),
		UpdatedAt:   pgTime(row.UpdatedAt),
	}
}

func decodeStringTags(raw any) ([]string, error) {
	switch v := raw.(type) {
	case nil:
		return []string{}, nil
	case []byte:
		var out []string
		if err := json.Unmarshal(v, &out); err != nil {
			return nil, err
		}
		return out, nil
	case string:
		var out []string
		if err := json.Unmarshal([]byte(v), &out); err != nil {
			return nil, err
		}
		return out, nil
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out, nil
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		var out []string
		if err := json.Unmarshal(b, &out); err != nil {
			return nil, err
		}
		return out, nil
	}
}
