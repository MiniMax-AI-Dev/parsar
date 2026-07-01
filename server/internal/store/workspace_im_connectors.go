package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
)

// ============================================================
// workspace_im_connectors — workspace 维度的 IM 连接器(feishu/slack/
// discord)。见 migration 000002 与 db/queries/store.sql。
//
// 设计要点:
//   - 凭据密文存在 secrets(vault),本表 config(jsonb) 只存 *_ref(UUID
//     指针)与非敏感字段(event_mode / intents 等)。
//   - app_id 是 workspace-bot 的通用 join key:配置保存时即可知,而
//     team_id(Slack)/guild_id(Discord)只有 Bot 入驻后才知道。
//   - (workspace_id, platform) 唯一 → 每个 workspace 每平台至多一个连接器;
//     (platform, app_id) 唯一 → 同一 app_id 不能被两个 workspace 占用,
//     冲突时 pg 报 23505,被映射成 *_app_id_in_use。
// ============================================================

const auditWorkspaceIMConnectorUpdated = "workspace.im_connector.updated"

// 连接器配置不完整 / app_id 冲突的错误哨兵。HTTP 层把它们映射成
// 422 *_connector_incomplete 与 409 *_app_id_in_use。
var (
	ErrSlackConnectorIncomplete   = errors.New("slack connector enabled requires app_id, bot_token_ref, and (socket: app_token_ref / events: signing_secret_ref)")
	ErrSlackAppIDInUse            = errors.New("another workspace has already registered this Slack bot app_id")
	ErrDiscordConnectorIncomplete = errors.New("discord connector enabled requires app_id and bot_token_ref")
	ErrDiscordAppIDInUse          = errors.New("another workspace has already registered this Discord bot app_id")
	ErrTeamsConnectorIncomplete   = errors.New("teams connector enabled requires app_id and app_password_ref")
	ErrTeamsAppIDInUse            = errors.New("another workspace has already registered this Teams bot app_id")
)

// WorkspaceConnectorChange 是三个 Upsert 方法的统一返回值。Config 已去掉
// 列字段(id/workspace_id/platform/app_id/enabled),只剩 *_ref 与模式。
type WorkspaceConnectorChange struct {
	ID          string         `json:"id"`
	WorkspaceID string         `json:"workspace_id"`
	Platform    string         `json:"platform"`
	AppID       string         `json:"app_id"`
	Enabled     bool           `json:"enabled"`
	Config      map[string]any `json:"config"`
	UpdatedAt   time.Time      `json:"updated_at"`

	// Noop=true 表示新配置与旧配置逐字段相等,handler 仍返回 200 但抑制审计。
	Noop bool `json:"noop,omitempty"`
}

// ------------------------------------------------------------
// Snapshots — 每平台 config 子树的扁平视图(含列字段以便逐字段比对 noop)。
// ------------------------------------------------------------

// WorkspaceSlackConnectorSnapshot mirrors a Slack connector's config plus
// its column fields (Enabled/AppID) for noop comparison.
type WorkspaceSlackConnectorSnapshot struct {
	Enabled          bool   `json:"enabled"`
	AppID            string `json:"app_id"`
	BotTokenRef      string `json:"bot_token_ref"`
	AppTokenRef      string `json:"app_token_ref"`
	SigningSecretRef string `json:"signing_secret_ref"`
	EventMode        string `json:"event_mode"`
}

func (s WorkspaceSlackConnectorSnapshot) isZero() bool {
	return !s.Enabled && s.AppID == "" && s.BotTokenRef == "" && s.AppTokenRef == "" &&
		s.SigningSecretRef == "" && (s.EventMode == "" || s.EventMode == "socket")
}

// toConfigMap returns only the jsonb-stored fields (column fields excluded).
func (s WorkspaceSlackConnectorSnapshot) toConfigMap() map[string]any {
	return map[string]any{
		"bot_token_ref":      s.BotTokenRef,
		"app_token_ref":      s.AppTokenRef,
		"signing_secret_ref": s.SigningSecretRef,
		"event_mode":         normalizeSlackEventMode(s.EventMode),
	}
}

// WorkspaceDiscordConnectorSnapshot mirrors a Discord connector's config plus
// its column fields.
type WorkspaceDiscordConnectorSnapshot struct {
	Enabled      bool   `json:"enabled"`
	AppID        string `json:"app_id"`
	BotTokenRef  string `json:"bot_token_ref"`
	PublicKeyRef string `json:"public_key_ref"`
	Intents      string `json:"intents"`
}

func (s WorkspaceDiscordConnectorSnapshot) isZero() bool {
	return !s.Enabled && s.AppID == "" && s.BotTokenRef == "" && s.PublicKeyRef == "" && s.Intents == ""
}

func (s WorkspaceDiscordConnectorSnapshot) toConfigMap() map[string]any {
	return map[string]any{
		"bot_token_ref":  s.BotTokenRef,
		"public_key_ref": s.PublicKeyRef,
		"intents":        s.Intents,
	}
}

// WorkspaceTeamsConnectorSnapshot mirrors a Teams connector's config plus its
// column fields. app_password_ref points at the AAD client secret used to mint
// the outbound Connector bearer; tenant_id is non-secret (empty = multi-tenant
// botframework.com authority).
type WorkspaceTeamsConnectorSnapshot struct {
	Enabled        bool   `json:"enabled"`
	AppID          string `json:"app_id"`
	AppPasswordRef string `json:"app_password_ref"`
	TenantID       string `json:"tenant_id"`
}

func (s WorkspaceTeamsConnectorSnapshot) isZero() bool {
	return !s.Enabled && s.AppID == "" && s.AppPasswordRef == "" && s.TenantID == ""
}

func (s WorkspaceTeamsConnectorSnapshot) toConfigMap() map[string]any {
	return map[string]any{
		"app_password_ref": s.AppPasswordRef,
		"tenant_id":        s.TenantID,
	}
}

// WorkspaceFeishuConnectorSnapshot mirrors a Feishu connector's config plus
// column fields. Distinct from the agent-dimension FeishuConnectorSnapshot.
type WorkspaceFeishuConnectorSnapshot struct {
	Enabled              bool   `json:"enabled"`
	AppID                string `json:"app_id"`
	AppSecretRef         string `json:"app_secret_ref"`
	VerificationTokenRef string `json:"verification_token_ref"`
	EncryptKeyRef        string `json:"encrypt_key_ref"`
	BotOpenID            string `json:"bot_open_id"`
	EventMode            string `json:"event_mode"`
}

func (s WorkspaceFeishuConnectorSnapshot) isZero() bool {
	return !s.Enabled && s.AppID == "" && s.AppSecretRef == "" && s.VerificationTokenRef == "" &&
		s.EncryptKeyRef == "" && s.BotOpenID == "" && (s.EventMode == "" || s.EventMode == "webhook")
}

func (s WorkspaceFeishuConnectorSnapshot) toConfigMap() map[string]any {
	return map[string]any{
		"app_secret_ref":         s.AppSecretRef,
		"verification_token_ref": s.VerificationTokenRef,
		"encrypt_key_ref":        s.EncryptKeyRef,
		"bot_open_id":            s.BotOpenID,
		"event_mode":             normalizeFeishuEventMode(s.EventMode),
	}
}

func normalizeSlackEventMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "events", "events_api", "http", "webhook":
		return "events"
	default:
		return "socket"
	}
}

// ------------------------------------------------------------
// Inputs
// ------------------------------------------------------------

type UpsertWorkspaceSlackConnectorInput struct {
	WorkspaceID      string
	Enabled          bool
	AppID            string
	BotTokenRef      string // xoxb-... secret ref
	AppTokenRef      string // xapp-... secret ref (socket mode)
	SigningSecretRef string // signing secret ref (events api mode)
	EventMode        string // socket (default) | events
}

type UpsertWorkspaceDiscordConnectorInput struct {
	WorkspaceID  string
	Enabled      bool
	AppID        string // discord application id
	BotTokenRef  string
	PublicKeyRef string // optional — interactions endpoint verification
	Intents      string // optional non-secret gateway intents bitmask/list
}

type UpsertWorkspaceTeamsConnectorInput struct {
	WorkspaceID    string
	Enabled        bool
	AppID          string // microsoft app id
	AppPasswordRef string // AAD client secret ref
	TenantID       string // optional — empty = multi-tenant botframework.com
}

type UpsertWorkspaceFeishuConnectorInput struct {
	WorkspaceID          string
	Enabled              bool
	AppID                string
	AppSecretRef         string
	VerificationTokenRef string
	EncryptKeyRef        string // optional — only when event encryption is on
	BotOpenID            string // required when enabled — @mention & self-sender recognition
	EventMode            string // websocket | webhook
}

// ------------------------------------------------------------
// Upserts
// ------------------------------------------------------------

// UpsertWorkspaceSlackConnector writes the Slack connector for a workspace.
// When Enabled, app_id + bot_token_ref are required plus app_token_ref
// (socket) or signing_secret_ref (events) → ErrSlackConnectorIncomplete.
// app_id collisions across workspaces surface as ErrSlackAppIDInUse.
func (s *Store) UpsertWorkspaceSlackConnector(ctx context.Context, input UpsertWorkspaceSlackConnectorInput, actorID string) (WorkspaceConnectorChange, error) {
	snap := WorkspaceSlackConnectorSnapshot{
		Enabled:          input.Enabled,
		AppID:            strings.TrimSpace(input.AppID),
		BotTokenRef:      strings.TrimSpace(input.BotTokenRef),
		AppTokenRef:      strings.TrimSpace(input.AppTokenRef),
		SigningSecretRef: strings.TrimSpace(input.SigningSecretRef),
		EventMode:        normalizeSlackEventMode(input.EventMode),
	}
	if snap.Enabled {
		if snap.AppID == "" || snap.BotTokenRef == "" {
			return WorkspaceConnectorChange{}, ErrSlackConnectorIncomplete
		}
		if snap.EventMode == "socket" && snap.AppTokenRef == "" {
			return WorkspaceConnectorChange{}, ErrSlackConnectorIncomplete
		}
		if snap.EventMode == "events" && snap.SigningSecretRef == "" {
			return WorkspaceConnectorChange{}, ErrSlackConnectorIncomplete
		}
	}
	return s.upsertWorkspaceConnector(ctx, workspaceConnectorUpsert{
		platform:    "slack",
		workspaceID: input.WorkspaceID,
		appID:       snap.AppID,
		enabled:     snap.Enabled,
		config:      snap.toConfigMap(),
		actorID:     actorID,
		appIDInUse:  ErrSlackAppIDInUse,
	})
}

// UpsertWorkspaceDiscordConnector writes the Discord connector for a workspace.
func (s *Store) UpsertWorkspaceDiscordConnector(ctx context.Context, input UpsertWorkspaceDiscordConnectorInput, actorID string) (WorkspaceConnectorChange, error) {
	snap := WorkspaceDiscordConnectorSnapshot{
		Enabled:      input.Enabled,
		AppID:        strings.TrimSpace(input.AppID),
		BotTokenRef:  strings.TrimSpace(input.BotTokenRef),
		PublicKeyRef: strings.TrimSpace(input.PublicKeyRef),
		Intents:      strings.TrimSpace(input.Intents),
	}
	if snap.Enabled {
		if snap.AppID == "" || snap.BotTokenRef == "" {
			return WorkspaceConnectorChange{}, ErrDiscordConnectorIncomplete
		}
	}
	return s.upsertWorkspaceConnector(ctx, workspaceConnectorUpsert{
		platform:    "discord",
		workspaceID: input.WorkspaceID,
		appID:       snap.AppID,
		enabled:     snap.Enabled,
		config:      snap.toConfigMap(),
		actorID:     actorID,
		appIDInUse:  ErrDiscordAppIDInUse,
	})
}

// UpsertWorkspaceTeamsConnector writes the Teams connector for a workspace.
// When Enabled, app_id + app_password_ref are required → ErrTeamsConnectorIncomplete.
// app_id collisions across workspaces surface as ErrTeamsAppIDInUse.
func (s *Store) UpsertWorkspaceTeamsConnector(ctx context.Context, input UpsertWorkspaceTeamsConnectorInput, actorID string) (WorkspaceConnectorChange, error) {
	snap := WorkspaceTeamsConnectorSnapshot{
		Enabled:        input.Enabled,
		AppID:          strings.TrimSpace(input.AppID),
		AppPasswordRef: strings.TrimSpace(input.AppPasswordRef),
		TenantID:       strings.TrimSpace(input.TenantID),
	}
	if snap.Enabled {
		if snap.AppID == "" || snap.AppPasswordRef == "" {
			return WorkspaceConnectorChange{}, ErrTeamsConnectorIncomplete
		}
	}
	return s.upsertWorkspaceConnector(ctx, workspaceConnectorUpsert{
		platform:    "teams",
		workspaceID: input.WorkspaceID,
		appID:       snap.AppID,
		enabled:     snap.Enabled,
		config:      snap.toConfigMap(),
		actorID:     actorID,
		appIDInUse:  ErrTeamsAppIDInUse,
	})
}

// UpsertWorkspaceFeishuConnector writes the Feishu connector for a workspace.
// Reuses ErrFeishuConnectorIncomplete / ErrFeishuAppIDInUse sentinels.
func (s *Store) UpsertWorkspaceFeishuConnector(ctx context.Context, input UpsertWorkspaceFeishuConnectorInput, actorID string) (WorkspaceConnectorChange, error) {
	snap := WorkspaceFeishuConnectorSnapshot{
		Enabled:              input.Enabled,
		AppID:                strings.TrimSpace(input.AppID),
		AppSecretRef:         strings.TrimSpace(input.AppSecretRef),
		VerificationTokenRef: strings.TrimSpace(input.VerificationTokenRef),
		EncryptKeyRef:        strings.TrimSpace(input.EncryptKeyRef),
		BotOpenID:            strings.TrimSpace(input.BotOpenID),
		EventMode:            normalizeFeishuEventMode(input.EventMode),
	}
	if snap.Enabled {
		// bot_open_id is required: it's how the inbound path recognizes an @Bot
		// mention (ShouldSkipGroupWithoutMention) and self-sender messages —
		// without it the bot silently drops group messages it should answer.
		if snap.AppID == "" || snap.AppSecretRef == "" || snap.BotOpenID == "" || (snap.EventMode != "websocket" && snap.VerificationTokenRef == "") {
			return WorkspaceConnectorChange{}, ErrFeishuConnectorIncomplete
		}
	}
	return s.upsertWorkspaceConnector(ctx, workspaceConnectorUpsert{
		platform:    "feishu",
		workspaceID: input.WorkspaceID,
		appID:       snap.AppID,
		enabled:     snap.Enabled,
		config:      snap.toConfigMap(),
		actorID:     actorID,
		appIDInUse:  ErrFeishuAppIDInUse,
	})
}

// workspaceConnectorUpsert is the shared driver behind the three typed
// Upsert methods. Keeps validation in the typed wrappers, persistence here.
type workspaceConnectorUpsert struct {
	platform    string
	workspaceID string
	appID       string
	enabled     bool
	config      map[string]any
	actorID     string
	appIDInUse  error // sentinel returned on (platform, app_id) collision
}

func (s *Store) upsertWorkspaceConnector(ctx context.Context, u workspaceConnectorUpsert) (WorkspaceConnectorChange, error) {
	wsID := strings.TrimSpace(u.workspaceID)
	if wsID == "" {
		return WorkspaceConnectorChange{}, fmt.Errorf("%w: empty workspace_id", ErrUnknownWorkspace)
	}
	wsUUID, err := uuid(wsID)
	if err != nil {
		return WorkspaceConnectorChange{}, err
	}

	// Read the current row (if any) for noop detection + before/after audit.
	oldAppID, oldEnabled, oldConfig, hadRow, err := s.currentWorkspaceConnector(ctx, wsUUID, u.platform)
	if err != nil {
		return WorkspaceConnectorChange{}, err
	}

	now := time.Now().UTC()
	encoded, err := json.Marshal(nonNilMap(u.config))
	if err != nil {
		return WorkspaceConnectorChange{}, err
	}

	row, err := sqlc.New(s.db).UpsertWorkspaceIMConnector(ctx, sqlc.UpsertWorkspaceIMConnectorParams{
		ID:          mustUUID(newID()),
		WorkspaceID: wsUUID,
		Platform:    u.platform,
		AppID:       u.appID,
		Enabled:     u.enabled,
		Config:      encoded,
		CreatedBy:   strings.TrimSpace(u.actorID),
		Now:         timestamptz(now),
	})
	if err != nil {
		if isUniqueViolation(err) {
			return WorkspaceConnectorChange{}, fmt.Errorf("%w: platform=%s app_id=%s", u.appIDInUse, u.platform, u.appID)
		}
		return WorkspaceConnectorChange{}, err
	}

	newConfig := decodeJSONMap(row.Config)
	noop := hadRow && oldEnabled == row.Enabled && oldAppID == row.AppID && jsonMapEqual(oldConfig, newConfig)

	change := WorkspaceConnectorChange{
		ID:          row.ID,
		WorkspaceID: row.WorkspaceID,
		Platform:    row.Platform,
		AppID:       row.AppID,
		Enabled:     row.Enabled,
		Config:      newConfig,
		UpdatedAt:   pgTime(row.UpdatedAt),
		Noop:        noop,
	}
	if !change.Noop {
		// Audit payload omits *_ref values; only presence/booleans.
		s.emitAgentAudit(now, u.actorID, auditWorkspaceIMConnectorUpdated, "workspace_im_connector", change.ID, change.WorkspaceID, map[string]any{
			"platform":    change.Platform,
			"old_enabled": oldEnabled,
			"new_enabled": change.Enabled,
			"old_app_id":  oldAppID,
			"new_app_id":  change.AppID,
		})
	}
	return change, nil
}

// currentWorkspaceConnector returns the existing connector row's app_id /
// enabled / config for the given platform, or hadRow=false when absent.
func (s *Store) currentWorkspaceConnector(ctx context.Context, wsUUID pgtype.UUID, platform string) (appID string, enabled bool, config map[string]any, hadRow bool, err error) {
	rows, err := sqlc.New(s.db).GetWorkspaceIMConnectors(ctx, wsUUID)
	if err != nil {
		return "", false, nil, false, err
	}
	for _, r := range rows {
		if r.Platform == platform {
			return r.AppID, r.Enabled, decodeJSONMap(r.Config), true, nil
		}
	}
	return "", false, map[string]any{}, false, nil
}

// ------------------------------------------------------------
// Reads
// ------------------------------------------------------------

// WorkspaceConnectorRead is the decoded view of one connector row.
type WorkspaceConnectorRead struct {
	ID            string         `json:"id"`
	WorkspaceID   string         `json:"workspace_id"`
	WorkspaceName string         `json:"workspace_name"`
	Platform      string         `json:"platform"`
	AppID         string         `json:"app_id"`
	Enabled       bool           `json:"enabled"`
	Config        map[string]any `json:"config"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

// GetWorkspaceIMConnectors returns all platforms' connectors for a workspace
// (drives the admin panel's initial state).
func (s *Store) GetWorkspaceIMConnectors(ctx context.Context, workspaceID string) ([]WorkspaceConnectorRead, error) {
	wsUUID, err := uuid(strings.TrimSpace(workspaceID))
	if err != nil {
		return nil, err
	}
	rows, err := sqlc.New(s.db).GetWorkspaceIMConnectors(ctx, wsUUID)
	if err != nil {
		return nil, err
	}
	out := make([]WorkspaceConnectorRead, 0, len(rows))
	for _, r := range rows {
		out = append(out, WorkspaceConnectorRead{
			ID:          r.ID,
			WorkspaceID: r.WorkspaceID,
			Platform:    r.Platform,
			AppID:       r.AppID,
			Enabled:     r.Enabled,
			Config:      decodeJSONMap(r.Config),
			CreatedAt:   pgTime(r.CreatedAt),
			UpdatedAt:   pgTime(r.UpdatedAt),
		})
	}
	return out, nil
}

// GetWorkspaceConnectorByAppID resolves one enabled connector by (platform,
// app_id) — the outbound resolver's reverse lookup to fetch the token ref.
func (s *Store) GetWorkspaceConnectorByAppID(ctx context.Context, platform, appID string) (WorkspaceConnectorRead, error) {
	row, err := sqlc.New(s.db).GetWorkspaceConnectorByAppID(ctx, sqlc.GetWorkspaceConnectorByAppIDParams{
		Platform: strings.TrimSpace(platform),
		AppID:    strings.TrimSpace(appID),
	})
	if err != nil {
		return WorkspaceConnectorRead{}, err
	}
	return WorkspaceConnectorRead{
		ID:            row.CID,
		WorkspaceID:   row.CWorkspaceID,
		WorkspaceName: row.WorkspaceName,
		Platform:      row.Platform,
		AppID:         row.AppID,
		Enabled:       row.Enabled,
		Config:        decodeJSONMap(row.Config),
		CreatedAt:     pgTime(row.CreatedAt),
		UpdatedAt:     pgTime(row.UpdatedAt),
	}, nil
}

// ListWorkspaceConnectorsByPlatform returns every enabled connector for a
// platform (with a non-empty app_id) — the inbound reconcilers' scan source.
func (s *Store) ListWorkspaceConnectorsByPlatform(ctx context.Context, platform string) ([]WorkspaceConnectorRead, error) {
	rows, err := sqlc.New(s.db).ListWorkspaceConnectorsByPlatform(ctx, strings.TrimSpace(platform))
	if err != nil {
		return nil, err
	}
	out := make([]WorkspaceConnectorRead, 0, len(rows))
	for _, r := range rows {
		out = append(out, WorkspaceConnectorRead{
			ID:            r.CID,
			WorkspaceID:   r.CWorkspaceID,
			WorkspaceName: r.WorkspaceName,
			Platform:      r.Platform,
			AppID:         r.AppID,
			Enabled:       r.Enabled,
			Config:        decodeJSONMap(r.Config),
			CreatedAt:     pgTime(r.CreatedAt),
			UpdatedAt:     pgTime(r.UpdatedAt),
		})
	}
	return out, nil
}

// jsonMapEqual compares two decoded jsonb maps by their canonical JSON
// encoding — enough for noop detection on flat *_ref/string configs.
func jsonMapEqual(a, b map[string]any) bool {
	ab, err1 := json.Marshal(nonNilMap(a))
	bb, err2 := json.Marshal(nonNilMap(b))
	if err1 != nil || err2 != nil {
		return false
	}
	return string(ab) == string(bb)
}
