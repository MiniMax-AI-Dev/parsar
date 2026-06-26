package store

import "testing"

func TestApplyTriggerMessagePrefix(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		metadata map[string]any
		content  string
		want     string
	}{
		{name: "nil-metadata", metadata: nil, content: "hello", want: "hello"},
		{name: "empty-metadata", metadata: map[string]any{}, content: "hello", want: "hello"},
		{
			name: "no-prefix-key",
			metadata: map[string]any{
				"chat_type": "group",
			},
			content: "hello",
			want:    "hello",
		},
		{
			name: "empty-prefix",
			metadata: map[string]any{
				TriggerMessageQuotedChainPrefixKey: "",
			},
			content: "hello",
			want:    "hello",
		},
		{
			name: "non-string-prefix",
			metadata: map[string]any{
				TriggerMessageQuotedChainPrefixKey: 42,
			},
			content: "hello",
			want:    "hello",
		},
		{
			name: "prepends-prefix",
			metadata: map[string]any{
				TriggerMessageQuotedChainPrefixKey: "[Quoted message]\nparent text\n[/Quoted message]\n",
			},
			content: "what does it mean?",
			want:    "[Quoted message]\nparent text\n[/Quoted message]\nwhat does it mean?",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := applyTriggerMessagePrefix(tc.metadata, tc.content)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
