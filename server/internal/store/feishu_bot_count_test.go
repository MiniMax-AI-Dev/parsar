package store

import (
	"context"
	"encoding/json"
	"testing"
)

// TestCountActiveFeishuBotAgents covers the matrix the OSS lazy-mode
// startup gate cares about: 0/1/many bots and the negative cases
// (connector disabled, agent disabled, soft-deleted).
func TestCountActiveFeishuBotAgents(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	ids := DefaultDevFixtureIDs()
	if _, err := New(db).InsertDevFixture(ctx, ids); err != nil {
		t.Fatal(err)
	}

	if n, err := New(db).CountActiveFeishuBotAgents(ctx); err != nil {
		t.Fatal(err)
	} else if n != 0 {
		t.Errorf("baseline count = %d, want 0", n)
	}

	enableBot := func(agentID, appID string, enabled bool) {
		t.Helper()
		cfg := map[string]any{
			"connectors": map[string]any{
				"feishu": map[string]any{
					"enabled": enabled,
					"app_id":  appID,
				},
			},
		}
		raw, _ := json.Marshal(cfg)
		if _, err := db.Exec(ctx, `update agents set config = $1::jsonb where id = $2::uuid`, raw, agentID); err != nil {
			t.Fatal(err)
		}
	}

	enableBot(ids.ProductAgentID, "cli_a", true)
	if n, err := New(db).CountActiveFeishuBotAgents(ctx); err != nil {
		t.Fatal(err)
	} else if n != 1 {
		t.Errorf("one-bot count = %d, want 1", n)
	}

	enableBot(ids.BackendAgentID, "cli_b", true)
	if n, err := New(db).CountActiveFeishuBotAgents(ctx); err != nil {
		t.Fatal(err)
	} else if n != 2 {
		t.Errorf("two-bot count = %d, want 2", n)
	}

	enableBot(ids.BackendAgentID, "cli_b", false)
	if n, err := New(db).CountActiveFeishuBotAgents(ctx); err != nil {
		t.Fatal(err)
	} else if n != 1 {
		t.Errorf("after disable count = %d, want 1", n)
	}

	if _, err := db.Exec(ctx, "update agents set deleted_at = now() where id = $1::uuid", ids.ProductAgentID); err != nil {
		t.Fatal(err)
	}
	if n, err := New(db).CountActiveFeishuBotAgents(ctx); err != nil {
		t.Fatal(err)
	} else if n != 0 {
		t.Errorf("after soft-delete count = %d, want 0", n)
	}
}
