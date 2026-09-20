package api

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestInitialFilesDecodeAndConfidentialMetadata(t *testing.T) {
	canary := "initial-private-canary"
	raw := `{"type":"openai_hosted","files":[{"type":"inline","path":"/workspace/input/data.txt","data":"` + base64.StdEncoding.EncodeToString([]byte(canary)) + `"},{"type":"file_id","path":"/workspace/source","file_id":"file-source"}]}`
	input, err := (decodedSessionRequest{Environment: json.RawMessage(raw)}).validated()
	if err != nil || len(input.initialFiles) != 2 || string(input.initialFiles[0].Data) != canary {
		t.Fatal("initial files decode", err)
	}
	safe, _ := json.Marshal(input.Environment)
	if strings.Contains(string(safe), canary) || strings.Contains(string(safe), base64.StdEncoding.EncodeToString([]byte(canary))) || strings.Contains(string(safe), `"data"`) {
		t.Fatal("confidential data entered ordinary configuration")
	}
	intent, err := sessionCreationRequest(input, nil)
	if err != nil || !strings.Contains(string(intent), `"data"`) {
		t.Fatal("creation intent lost confidential input identity")
	}
	for _, files := range []string{
		`[{"type":"inline","path":"/workspace/a","data":null}]`,
		`[{"type":"inline","path":"/workspace/a","data":"notbase64"}]`,
		`[{"type":"inline","path":"/workspace/../secret","data":""}]`,
		`[{"type":"inline","path":"/tmp/secret","data":""}]`,
		`[{"type":"inline","path":"/workspace/a","data":"","file_id":null}]`,
		`[{"type":"file_id","path":"/workspace/a","file_id":"x","data":null}]`,
		`[{"type":"inline","path":"/workspace/a","data":""},{"type":"inline","path":"/workspace/a","data":""}]`,
	} {
		if _, err := decodeInitialFiles(json.RawMessage(files)); err == nil {
			t.Fatal("invalid initial files accepted", files)
		}
	}
	for _, value := range []string{"null", "[]", `[{"type":"inline","path":"/workspace/a","data":""}]`} {
		if _, _, _, err := decodeTemplateEnvironment(json.RawMessage(`{"type":"openai_hosted","environment_template_id":"template","files":` + value + `}`)); err == nil {
			t.Fatal("unconfirmed template file override accepted")
		}
	}
}
