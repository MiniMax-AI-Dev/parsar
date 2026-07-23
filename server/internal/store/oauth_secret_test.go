package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestCapabilitySecretIsScopedToWorkspaceAndCanRotate(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	st := New(db)
	ids := mustSeedDevFixture(t, ctx, st)

	created, err := st.CreateSecret(ctx, CreateSecretInput{
		WorkspaceID:        ids.WorkspaceID,
		Name:               "Notion OAuth",
		Kind:               "capability_inline",
		Provider:           "notion",
		AuthType:           "oauth2",
		Masked:             "configured",
		CreatedBy:          ids.UserID,
		CredentialKindCode: "notion_mcp_oauth",
	}, []byte(`{"token":"first"}`))
	if err != nil {
		t.Fatalf("CreateSecret: %v", err)
	}
	if created.Provider != "notion" || created.Metadata["workspace_id"] != ids.WorkspaceID {
		t.Fatalf("secret = %+v", created)
	}

	otherWorkspaceID := "00000000-0000-0000-0000-000000000099"
	visible, err := st.ListSecrets(ctx, otherWorkspaceID, 100)
	if err != nil {
		t.Fatalf("ListSecrets: %v", err)
	}
	for _, secret := range visible {
		if secret.ID == created.ID {
			t.Fatalf("workspace-scoped secret leaked into another workspace")
		}
	}
	if _, err := st.GetSecretPayload(ctx, otherWorkspaceID, created.ID); !errors.Is(err, ErrUnknownSecret) {
		t.Fatalf("cross-workspace GetSecretPayload error = %v", err)
	}

	updated, err := st.UpdateSecretPayload(ctx, ids.WorkspaceID, created.ID, []byte(`{"token":"second"}`))
	if err != nil {
		t.Fatalf("UpdateSecretPayload: %v", err)
	}
	var payload map[string]string
	if err := json.Unmarshal(updated.EncryptedPayload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["token"] != "second" {
		t.Fatalf("updated payload = %+v", payload)
	}
}
