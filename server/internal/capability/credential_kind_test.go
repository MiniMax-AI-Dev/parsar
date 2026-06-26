package capability

import "testing"

func TestCredentialKindLookup(t *testing.T) {
	meta, ok := Lookup("github_pat")
	if !ok {
		t.Fatal("expected github_pat to be registered")
	}
	if meta.Name != "github_pat" || meta.ZhCN != "GitHub 访问令牌" || meta.EnUS != "GitHub Access Token" {
		t.Fatalf("unexpected github_pat meta: %+v", meta)
	}
	if meta.Placeholder != "ghp_… / github_pat_… / gho_…" || meta.GetURL != "https://github.com/settings/tokens" {
		t.Fatalf("unexpected github_pat helper fields: %+v", meta)
	}
	if _, ok := Lookup("unknown_kind"); ok {
		t.Fatal("unknown_kind should not be registered")
	}
}

func TestCredentialKindListOrderStable(t *testing.T) {
	want := []string{"github_pat", "slack_bot_token", "postgres_dsn", "notion_integration", "jira_api_token"}
	got := List()
	if len(got) != len(want) {
		t.Fatalf("List length = %d, want %d", len(got), len(want))
	}
	for i, meta := range got {
		if meta.Name != want[i] {
			t.Fatalf("List()[%d].Name = %q, want %q", i, meta.Name, want[i])
		}
	}
}

func TestCredentialKindRegistryComplete(t *testing.T) {
	want := map[string]CredentialKindMeta{
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
	if len(CredentialKindRegistry) != len(want) {
		t.Fatalf("registry length = %d, want %d", len(CredentialKindRegistry), len(want))
	}
	for name, wantMeta := range want {
		got, ok := Lookup(name)
		if !ok {
			t.Fatalf("%s missing from registry", name)
		}
		if got != wantMeta {
			t.Fatalf("%s meta = %+v, want %+v", name, got, wantMeta)
		}
	}
}

func TestCredentialKindNames(t *testing.T) {
	want := []string{"github_pat", "slack_bot_token", "postgres_dsn", "notion_integration", "jira_api_token"}
	got := Names()
	if len(got) != len(want) {
		t.Fatalf("Names length = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Names()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	got[0] = "mutated"
	if Names()[0] != "github_pat" {
		t.Fatal("Names should return a defensive copy")
	}
}
