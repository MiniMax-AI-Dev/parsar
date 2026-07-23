package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

type builtInCredentialKindSeed struct {
	Code        string
	DisplayName string
	Description string
	Source      string
}

var builtInCredentialKindSeeds = []builtInCredentialKindSeed{
	{Code: "github_pat", DisplayName: "GitHub \u8bbf\u95ee\u4ee4\u724c", Description: "GitHub Personal Access Token", Source: CredentialKindSourcePlatformOAuth},
	{Code: "slack_bot_token", DisplayName: "Slack Bot Token", Description: "Slack Bot Token (xoxb-...)", Source: CredentialKindSourceUserDefined},
	{Code: "teams_app_password", DisplayName: "Teams App Password", Description: "Microsoft Teams Bot AAD client secret", Source: CredentialKindSourceUserDefined},
	{Code: "postgres_dsn", DisplayName: "Postgres \u8fde\u63a5\u4e32", Description: "Postgres DSN", Source: CredentialKindSourceUserDefined},
	{Code: "notion_integration", DisplayName: "Notion \u96c6\u6210 token", Description: "Notion Integration Token", Source: CredentialKindSourceUserDefined},
	{Code: "notion_mcp_oauth", DisplayName: "Notion MCP OAuth", Description: "Notion MCP OAuth access token", Source: CredentialKindSourcePlatformOAuth},
	{Code: "jira_api_token", DisplayName: "Jira API Token", Description: "Atlassian Jira API Token", Source: CredentialKindSourceUserDefined},
	{Code: "openai_api_key", DisplayName: "OpenAI API Key", Description: "Personal OpenAI API key (sk-...)", Source: CredentialKindSourcePlatformModel},
	{Code: "anthropic_api_key", DisplayName: "Anthropic API Key", Description: "Personal Anthropic API key (sk-ant-...)", Source: CredentialKindSourcePlatformModel},
	{Code: "deepseek_api_key", DisplayName: "DeepSeek API Key", Description: "Personal DeepSeek API key", Source: CredentialKindSourcePlatformModel},
	{Code: "internal_gw_api_key", DisplayName: "Internal Gateway API Key", Description: "Personal credential for the internal LLM gateway", Source: CredentialKindSourcePlatformModel},
}

var SupportedCredentialKinds = []string{
	"github_pat",
	"slack_bot_token",
	"teams_app_password",
	"postgres_dsn",
	"notion_integration",
	"notion_mcp_oauth",
	"jira_api_token",
	"openai_api_key",
	"anthropic_api_key",
	"deepseek_api_key",
	"internal_gw_api_key",
}

var supportedCredentialKindSet = func() map[string]struct{} {
	out := make(map[string]struct{}, len(SupportedCredentialKinds))
	for _, kind := range SupportedCredentialKinds {
		out[kind] = struct{}{}
	}
	return out
}()

func IsSupportedCredentialKind(kind string) bool {
	_, ok := supportedCredentialKindSet[strings.ToLower(strings.TrimSpace(kind))]
	return ok
}

func normalizeCredentialKindCode(kind string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(kind))
	if normalized == "" {
		return "", fmt.Errorf("%w: empty", ErrInvalidCredentialKind)
	}
	return normalized, nil
}

func (s *Store) normalizeRegisteredCredentialKind(ctx context.Context, kind string) (string, error) {
	normalized, err := normalizeCredentialKindCode(kind)
	if err != nil {
		return "", err
	}
	if _, err := s.GetCredentialKindByCode(ctx, normalized); err != nil {
		if errors.Is(err, ErrCredentialKindNotFound) {
			return "", fmt.Errorf("%w: %s", ErrInvalidCredentialKind, normalized)
		}
		return "", err
	}
	return normalized, nil
}

// normalizeRequiredCredentials trims/normalizes fields and rejects
// duplicate kinds. Registry validation lives in
// normalizeRegisteredRequiredCredentials.
func normalizeRequiredCredentials(creds []RequiredCredential) ([]RequiredCredential, error) {
	out := make([]RequiredCredential, 0, len(creds))
	seen := make(map[string]struct{}, len(creds))
	for _, rc := range creds {
		kind, err := normalizeCredentialKindCode(rc.Kind)
		if err != nil {
			return nil, err
		}
		if _, dup := seen[kind]; dup {
			return nil, fmt.Errorf("%w: duplicate kind %s", ErrInvalidCredentialKind, kind)
		}
		seen[kind] = struct{}{}
		out = append(out, RequiredCredential{
			Kind:        kind,
			Required:    rc.Required,
			Description: strings.TrimSpace(rc.Description),
		})
	}
	return out, nil
}

func (s *Store) normalizeRegisteredRequiredCredentials(ctx context.Context, creds []RequiredCredential) ([]RequiredCredential, error) {
	normalized, err := normalizeRequiredCredentials(creds)
	if err != nil {
		return nil, err
	}
	for _, rc := range normalized {
		if _, err := s.GetCredentialKindByCode(ctx, rc.Kind); err != nil {
			if errors.Is(err, ErrCredentialKindNotFound) {
				return nil, fmt.Errorf("%w: %s", ErrInvalidCredentialKind, rc.Kind)
			}
			return nil, err
		}
	}
	return normalized, nil
}

func seedBuiltInCredentialKinds(ctx context.Context, db sqlc.DBTX) (int64, error) {
	var inserted int64
	for _, kind := range builtInCredentialKindSeeds {
		tag, err := db.Exec(ctx, `
			insert into credential_kinds(
				id, code, display_name, description,
				value_schema, built_in, source, created_at, updated_at
			)
			values (
				gen_random_uuid(), $1, $2, $3,
				'{}'::jsonb, true, $4, now(), now()
			)
			on conflict (code) where deleted_at is null do nothing
		`, kind.Code, kind.DisplayName, kind.Description, kind.Source)
		if err != nil {
			return inserted, fmt.Errorf("seed credential_kind %q: %w", kind.Code, err)
		}
		inserted += tag.RowsAffected()
	}
	return inserted, nil
}
