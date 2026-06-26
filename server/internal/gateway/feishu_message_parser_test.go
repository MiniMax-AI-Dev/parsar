package gateway

import (
	"reflect"
	"strings"
	"testing"
)

func TestFeishuFetchedMessageText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		msgType       string
		body          string
		want          string   // exact match when set
		wantParts     []string // substring matches when set
		wantImageKeys []string // exact match (nil == no images expected)
	}{
		{name: "text", msgType: "text", body: `{"text":"hello world"}`, want: "hello world"},
		{name: "text-bare-string", msgType: "text", body: "raw legacy text", want: "raw legacy text"},
		{name: "text-blank", msgType: "text", body: `{"text":"   "}`, want: ""},
		{name: "empty-body", msgType: "text", body: "", want: ""},
		{
			name:    "post",
			msgType: "post",
			body: `{"zh_cn":{"title":"t","content":[
				[{"tag":"text","text":"first line"}],
				[{"tag":"text","text":"link "},{"tag":"a","text":"go","href":"https://x.example"}]
			]}}`,
			wantParts: []string{"first line", "[go](https://x.example)"},
		},
		{
			name:      "post-en-fallback",
			msgType:   "post",
			body:      `{"en_us":{"title":"t","content":[[{"tag":"text","text":"en only"}]]}}`,
			wantParts: []string{"en only"},
		},
		{
			// post body with embedded <img> nodes: text + image_keys both
			// surface, in the on-screen image order.
			name:    "post-with-img",
			msgType: "post",
			body: `{"zh_cn":{"title":"t","content":[
				[{"tag":"text","text":"see this"},{"tag":"img","image_key":"img_p1"}],
				[{"tag":"img","image_key":"img_p2"},{"tag":"text","text":"and this"}]
			]}}`,
			wantParts:     []string{"see this", "and this", "[image]"},
			wantImageKeys: []string{"img_p1", "img_p2"},
		},
		// image hops are now first-class: the walker takes the image_key
		// and lets the manager layer download + render an [image:N]
		// placeholder. text stays "" because the body has no readable text.
		{name: "image", msgType: "image", body: `{"image_key":"img_xyz"}`, want: "", wantImageKeys: []string{"img_xyz"}},
		{name: "image-missing-key", msgType: "image", body: `{"foo":"bar"}`, want: ""},
		// interactive: the raw card JSON is the fallback. LLMs digest
		// it fine — the P2P inbound path ships this shape to the agent
		// via ParseFeishuMessageContent's default fallback, and the
		// quote-chain path needs to match so cards-in-reply behave the
		// same as cards-as-DM.
		{name: "interactive", msgType: "interactive", body: `{"schema":"2.0","body":{"elements":[{"tag":"markdown","content":"hi"}]}}`, wantParts: []string{`"schema":"2.0"`, `"markdown"`, `"content":"hi"`}},
		{name: "merge-forward", msgType: "merge_forward", body: `{}`, want: ""},
		{name: "unknown-type", msgType: "share_chat", body: `{}`, want: ""},
		{name: "malformed-post", msgType: "post", body: "not json", want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, gotKeys := FeishuFetchedMessageText(tc.msgType, tc.body)
			if !reflect.DeepEqual(gotKeys, tc.wantImageKeys) {
				t.Errorf("image_keys: got %#v, want %#v", gotKeys, tc.wantImageKeys)
			}
			if len(tc.wantParts) > 0 {
				for _, part := range tc.wantParts {
					if !strings.Contains(got, part) {
						t.Errorf("missing %q in %q", part, got)
					}
				}
				return
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFeishuFetchedMessageText_InteractiveTruncatesOversize(t *testing.T) {
	// Build a card whose raw body is 2x the fallback cap. The truncation
	// marker must appear and the output must be <= cap + marker length.
	pad := strings.Repeat("X", feishuInteractiveRawFallbackMaxBytes*2)
	body := `{"title":"big","note":"` + pad + `"}`

	got, gotKeys := FeishuFetchedMessageText("interactive", body)
	if gotKeys != nil {
		t.Errorf("interactive should not surface image_keys, got %#v", gotKeys)
	}
	if !strings.HasSuffix(got, "…[card truncated]") {
		t.Errorf("missing truncation marker; tail = %q", got[len(got)-min(40, len(got)):])
	}
	if !strings.HasPrefix(got, `{"title":"big"`) {
		t.Errorf("truncation lost the prefix; head = %q", got[:min(40, len(got))])
	}
	if len(got) > feishuInteractiveRawFallbackMaxBytes+len("…[card truncated]") {
		t.Errorf("got %d bytes, want <= cap+marker", len(got))
	}
}
