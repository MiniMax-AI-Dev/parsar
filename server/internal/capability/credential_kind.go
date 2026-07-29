package capability

type CredentialKindMeta struct {
	Name        string
	ZhCN        string
	EnUS        string
	Placeholder string
	GetURL      string
}

var CredentialKindRegistry = map[string]CredentialKindMeta{
	"github_pat": {
		Name:        "github_pat",
		ZhCN:        "GitHub 访问令牌",
		EnUS:        "GitHub Access Token",
		Placeholder: "ghp_… / github_pat_… / gho_…",
		GetURL:      "https://github.com/settings/tokens",
	},
	"slack_bot_token": {
		Name:        "slack_bot_token",
		ZhCN:        "Slack Bot Token",
		EnUS:        "Slack Bot Token",
		Placeholder: "xoxb-…",
		GetURL:      "https://api.slack.com/apps",
	},
	"postgres_dsn": {
		Name:        "postgres_dsn",
		ZhCN:        "Postgres 连接串",
		EnUS:        "Postgres DSN",
		Placeholder: "postgres://user:pass@host:5432/db",
	},
	"notion_integration": {
		Name:        "notion_integration",
		ZhCN:        "Notion 集成 token",
		EnUS:        "Notion Integration Token",
		Placeholder: "secret_…",
		GetURL:      "https://www.notion.so/profile/integrations",
	},
	"jira_api_token": {
		Name:        "jira_api_token",
		ZhCN:        "Jira API Token",
		EnUS:        "Jira API Token",
		Placeholder: "ATATT…",
		GetURL:      "https://id.atlassian.com/manage-profile/security/api-tokens",
	},
}

var credentialKindNames = []string{
	"github_pat",
	"slack_bot_token",
	"postgres_dsn",
	"notion_integration",
	"jira_api_token",
}

func Lookup(kind string) (CredentialKindMeta, bool) {
	meta, ok := CredentialKindRegistry[kind]
	return meta, ok
}

func List() []CredentialKindMeta {
	out := make([]CredentialKindMeta, 0, len(credentialKindNames))
	for _, name := range credentialKindNames {
		out = append(out, CredentialKindRegistry[name])
	}
	return out
}

func Names() []string {
	out := make([]string, len(credentialKindNames))
	copy(out, credentialKindNames)
	return out
}
