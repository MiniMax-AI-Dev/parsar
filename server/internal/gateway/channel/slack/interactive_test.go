package slack

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// genericCard decodes an interactive Block Kit payload into a loosely-typed
// shape so the tests can walk buttons / selects / inputs regardless of block
// kind. The progress/terminal cards have their own typed decoder (decodeCard);
// the interactive cards mix block shapes, so a map walk is the honest tool.
type genericCard struct {
	Text   string           `json:"text"`
	Blocks []map[string]any `json:"blocks"`
}

func decodeInteractive(t *testing.T, card channel.Card) genericCard {
	t.Helper()
	if card.MIME != slackCardMIME {
		t.Fatalf("MIME = %q, want %q", card.MIME, slackCardMIME)
	}
	var gc genericCard
	if err := json.Unmarshal(card.Payload, &gc); err != nil {
		t.Fatalf("payload is not valid Block Kit JSON: %v\n%s", err, card.Payload)
	}
	return gc
}

// collectButtons returns every button element as (action_id, value, style).
func collectButtons(gc genericCard) []map[string]string {
	var out []map[string]string
	for _, b := range gc.Blocks {
		els, _ := b["elements"].([]any)
		for _, e := range els {
			el, _ := e.(map[string]any)
			if t, _ := el["type"].(string); t == "button" {
				out = append(out, map[string]string{
					"action_id": str(el["action_id"]),
					"value":     str(el["value"]),
					"style":     str(el["style"]),
				})
			}
		}
	}
	return out
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// findElementType reports whether any actions block carries an element of the
// given type, returning its action_id.
func findElementType(gc genericCard, elemType string) (string, bool) {
	for _, b := range gc.Blocks {
		els, _ := b["elements"].([]any)
		for _, e := range els {
			el, _ := e.(map[string]any)
			if t, _ := el["type"].(string); t == elemType {
				return str(el["action_id"]), true
			}
		}
	}
	return "", false
}

func TestRenderPermission_Buttons(t *testing.T) {
	card, err := newTestChannel().RenderPermission(context.Background(), channel.ReplyTarget{}, channel.PermissionRequest{
		Title:     "Demo Agent",
		ToolName:  "Bash",
		ToolInput: "rm -rf /tmp/x",
		RequestID: "perm-req-1",
	})
	if err != nil {
		t.Fatalf("RenderPermission: %v", err)
	}
	gc := decodeInteractive(t, card)

	if gc.Blocks[0]["type"] != "header" {
		t.Fatalf("first block must be header, got %v", gc.Blocks[0]["type"])
	}
	buttons := collectButtons(gc)
	if len(buttons) != 2 {
		t.Fatalf("buttons = %d, want 2 (allow/deny)", len(buttons))
	}
	allow, deny := buttons[0], buttons[1]
	if allow["action_id"] != "permission_allow" || allow["value"] != "perm-req-1" || allow["style"] != "primary" {
		t.Errorf("allow button = %+v", allow)
	}
	if deny["action_id"] != "permission_deny" || deny["value"] != "perm-req-1" || deny["style"] != "danger" {
		t.Errorf("deny button = %+v", deny)
	}
	// Every action_id must round-trip to a known neutral kind.
	if ActionKindFor("permission_allow") != channel.CardActionPermissionAllow {
		t.Errorf("permission_allow does not map to CardActionPermissionAllow")
	}
	if ActionKindFor("permission_deny") != channel.CardActionPermissionDeny {
		t.Errorf("permission_deny does not map to CardActionPermissionDeny")
	}
}

func TestRenderChoiceForm_SelectsAndSubmit(t *testing.T) {
	card, err := newTestChannel().RenderChoiceForm(context.Background(), channel.ReplyTarget{}, channel.ChoiceForm{
		Title:     "Pick options",
		RequestID: "pfuc-1",
		Questions: []channel.ChoiceQuestion{
			{Header: "Q1", Question: "Single?", MultiSelect: false, Options: []string{"a", "b"}},
			{Header: "Q2", Question: "Many?", MultiSelect: true, Options: []string{"x", "y", "z"}},
		},
	})
	if err != nil {
		t.Fatalf("RenderChoiceForm: %v", err)
	}
	gc := decodeInteractive(t, card)

	if _, ok := findElementType(gc, "static_select"); !ok {
		t.Errorf("single-select question must render a static_select; blocks=%s", card.Payload)
	}
	if _, ok := findElementType(gc, "multi_static_select"); !ok {
		t.Errorf("multi-select question must render a multi_static_select; blocks=%s", card.Payload)
	}
	pickID, _ := findElementType(gc, "static_select")
	if pickID != "ask_user_choice_pick" {
		t.Errorf("select action_id = %q, want ask_user_choice_pick", pickID)
	}
	buttons := collectButtons(gc)
	if len(buttons) != 1 {
		t.Fatalf("buttons = %d, want 1 (submit)", len(buttons))
	}
	if buttons[0]["action_id"] != "ask_user_choice_submit" || buttons[0]["value"] != "pfuc-1" {
		t.Errorf("submit button = %+v, want ask_user_choice_submit / pfuc-1", buttons[0])
	}
	if ActionKindFor("ask_user_choice_submit") != channel.CardActionUserChoiceSubmit {
		t.Errorf("ask_user_choice_submit does not map to CardActionUserChoiceSubmit")
	}
}

func TestRenderCredentialForm_InputsAndSubmit(t *testing.T) {
	card, err := newTestChannel().RenderCredentialForm(context.Background(), channel.ReplyTarget{}, channel.CredentialForm{
		Title: "Add credentials",
		Qkey:  "qkey-abc",
		Fields: []gateway.CredentialFormField{
			{Kind: "token", Label: "API Token", CapabilityName: "github", Placeholder: "ghp_…"},
			{Kind: "token", Label: "Slack Token", CapabilityName: "slack"},
		},
	})
	if err != nil {
		t.Fatalf("RenderCredentialForm: %v", err)
	}
	gc := decodeInteractive(t, card)

	var inputBlockIDs []string
	for _, b := range gc.Blocks {
		if b["type"] == "input" {
			inputBlockIDs = append(inputBlockIDs, str(b["block_id"]))
			el, _ := b["element"].(map[string]any)
			if str(el["type"]) != "plain_text_input" {
				t.Errorf("input element type = %v, want plain_text_input", el["type"])
			}
		}
	}
	if len(inputBlockIDs) != 2 {
		t.Fatalf("input blocks = %d, want 2", len(inputBlockIDs))
	}
	if inputBlockIDs[0] != "github" || inputBlockIDs[1] != "slack" {
		t.Errorf("input block_ids = %v, want [github slack] (capability names)", inputBlockIDs)
	}

	buttons := collectButtons(gc)
	if len(buttons) != 1 {
		t.Fatalf("buttons = %d, want 1 (submit)", len(buttons))
	}
	if buttons[0]["action_id"] != "credential_form_submit" || buttons[0]["value"] != "qkey-abc" {
		t.Errorf("submit button = %+v, want credential_form_submit / qkey-abc", buttons[0])
	}
	if ActionKindFor("credential_form_submit") != channel.CardActionCredentialSubmit {
		t.Errorf("credential_form_submit does not map to CardActionCredentialSubmit")
	}
}

// TestInteractiveCardsHaveFallback guards the notification fallback text so a
// stripped-blocks client still shows something meaningful.
func TestInteractiveCardsHaveFallback(t *testing.T) {
	c := newTestChannel()
	perm, _ := c.RenderPermission(context.Background(), channel.ReplyTarget{}, channel.PermissionRequest{Title: "T", ToolName: "Bash", RequestID: "r"})
	if decodeInteractive(t, perm).Text == "" {
		t.Error("permission card missing fallback text")
	}
	choice, _ := c.RenderChoiceForm(context.Background(), channel.ReplyTarget{}, channel.ChoiceForm{Title: "T", RequestID: "r"})
	if decodeInteractive(t, choice).Text == "" {
		t.Error("choice card missing fallback text")
	}
	cred, _ := c.RenderCredentialForm(context.Background(), channel.ReplyTarget{}, channel.CredentialForm{Title: "T", Qkey: "q"})
	if decodeInteractive(t, cred).Text == "" {
		t.Error("credential card missing fallback text")
	}
}
