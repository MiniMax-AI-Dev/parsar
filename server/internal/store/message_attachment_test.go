package store

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestDecodeMessageAttachments_HappyPath(t *testing.T) {
	t.Parallel()
	raw := map[string]any{
		"attachments": []any{
			map[string]any{
				"kind":        "image",
				"mime":        "image/png",
				"size":        float64(1234),
				"data_base64": "AAAA",
			},
			map[string]any{
				"kind":        "image",
				"mime":        "image/jpeg",
				"data_base64": "BBBB",
			},
		},
	}
	got := DecodeMessageAttachments(raw)
	want := []MessageAttachment{
		{Kind: "image", MIME: "image/png", Size: 1234, DataBase64: "AAAA"},
		{Kind: "image", MIME: "image/jpeg", DataBase64: "BBBB"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DecodeMessageAttachments mismatch:\n got=%#v\nwant=%#v", got, want)
	}
}

func TestDecodeMessageAttachments_SkipsMalformed(t *testing.T) {
	t.Parallel()
	raw := map[string]any{
		"attachments": []any{
			"bare-string-not-a-map",
			map[string]any{"kind": "", "data_base64": "AAAA"},
			map[string]any{"kind": "image", "data_base64": ""},
			map[string]any{"kind": "image", "data_base64": "OK"},
		},
	}
	got := DecodeMessageAttachments(raw)
	if len(got) != 1 || got[0].Kind != "image" || got[0].DataBase64 != "OK" {
		t.Fatalf("DecodeMessageAttachments: expected one valid attachment, got %#v", got)
	}
}

func TestDecodeMessageAttachments_NoAttachmentsKey(t *testing.T) {
	t.Parallel()
	cases := []map[string]any{
		nil,
		{},
		{"other": "value"},
		{"attachments": []any{}},
	}
	for i, c := range cases {
		if got := DecodeMessageAttachments(c); got != nil {
			t.Errorf("case %d: expected nil, got %#v", i, got)
		}
	}
}

func TestDecodeMessageAttachments_SizeFromJSONNumber(t *testing.T) {
	t.Parallel()
	// json.Decoder with UseNumber yields json.Number, not float64.
	jsonStr := `{"attachments":[{"kind":"image","data_base64":"AA","size":42}]}`
	dec := json.NewDecoder(stringReader(jsonStr))
	dec.UseNumber()
	var raw map[string]any
	if err := dec.Decode(&raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := DecodeMessageAttachments(raw)
	if len(got) != 1 || got[0].Size != 42 {
		t.Fatalf("expected Size=42, got %#v", got)
	}
}

func TestEncodeMessageAttachments_RoundTrip(t *testing.T) {
	t.Parallel()
	src := []MessageAttachment{
		{Kind: "image", MIME: "image/png", Size: 1024, DataBase64: "AAAA"},
		{Kind: "image", MIME: "", Size: 0, DataBase64: "BBBB"},
		{Kind: "", DataBase64: "skip-me"},
		{Kind: "image", DataBase64: ""},
	}
	encoded := EncodeMessageAttachments(src)
	if len(encoded) != 2 {
		t.Fatalf("expected 2 surviving entries, got %d: %#v", len(encoded), encoded)
	}
	// Lossy fields (empty MIME, zero Size) should stay dropped on the way back.
	wrapped := map[string]any{"attachments": toAnySlice(encoded)}
	back := DecodeMessageAttachments(wrapped)
	want := []MessageAttachment{
		{Kind: "image", MIME: "image/png", Size: 1024, DataBase64: "AAAA"},
		{Kind: "image", DataBase64: "BBBB"},
	}
	if !reflect.DeepEqual(back, want) {
		t.Fatalf("round trip mismatch:\n got=%#v\nwant=%#v", back, want)
	}
}

func TestEncodeMessageAttachments_NilOnEmpty(t *testing.T) {
	t.Parallel()
	if got := EncodeMessageAttachments(nil); got != nil {
		t.Errorf("nil input: expected nil, got %#v", got)
	}
	if got := EncodeMessageAttachments([]MessageAttachment{}); got != nil {
		t.Errorf("empty input: expected nil, got %#v", got)
	}
	if got := EncodeMessageAttachments([]MessageAttachment{{Kind: "", DataBase64: ""}}); got != nil {
		t.Errorf("all-invalid input: expected nil, got %#v", got)
	}
}

// stringReader keeps the json.Decoder test free of strings.NewReader imports.
type stringReader string

func (s stringReader) Read(p []byte) (int, error) {
	n := copy(p, s)
	if n == 0 {
		return 0, errEOF
	}
	return n, nil
}

var errEOF = ioErr("EOF")

type ioErr string

func (e ioErr) Error() string { return string(e) }

func toAnySlice(m []map[string]any) []any {
	out := make([]any, len(m))
	for i, v := range m {
		out[i] = v
	}
	return out
}
