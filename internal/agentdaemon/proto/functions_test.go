package proto

import (
	"encoding/json"
	"testing"
)

func TestFunctionResultContentWire(t *testing.T) {
	for _, content := range []string{
		`[]`,
		`[{"type":"input_text","text":""}]`,
		`[{"type":"input_text","text":"before"},{"type":"input_image","image_url":"data:image/png;base64,AA=="},{"type":"input_text","text":"after"}]`,
	} {
		raw := `{"delivery_id":"delivery","call_id":"call","success":false,"content":` + content + `}`
		var result FunctionResultPayload
		if err := json.Unmarshal([]byte(raw), &result); err != nil {
			t.Fatal(err)
		}
		if err := result.ValidateContent(); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(result)
		if err != nil || string(encoded) != raw {
			t.Fatalf("round trip: %s, %v", encoded, err)
		}
	}
	for _, content := range []string{
		`null`, `[null]`, `[{}]`,
		`[{"type":"input_text"}]`, `[{"type":"input_text","text":null}]`,
		`[{"type":"input_image"}]`, `[{"type":"input_image","image_url":null}]`,
		`[{"type":"input_text","text":"before","image_url":"url"}]`,
		`[{"type":"input_image","image_url":"url","text":"text"}]`,
		`[{"type":"input_audio","text":"audio"}]`,
	} {
		var result FunctionResultPayload
		if err := json.Unmarshal([]byte(`{"content":`+content+`}`), &result); err != nil {
			t.Fatal(err)
		}
		if err := result.ValidateContent(); err == nil {
			t.Fatalf("accepted %s", content)
		}
	}
}
