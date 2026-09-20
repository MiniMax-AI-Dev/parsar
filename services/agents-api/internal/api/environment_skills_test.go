package api

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"testing"
)

func skillInput(t *testing.T, body string) json.RawMessage {
	t.Helper()
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	file, err := writer.Create("proof/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.Write([]byte("---\nname: proof\ndescription: A proof.\n---\n" + body)); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"type": "inline", "name": "proof", "description": "A proof.", "source": map[string]string{"type": "base64", "media_type": "application/zip", "data": base64.StdEncoding.EncodeToString(archive.Bytes())}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestInlineSkillsSharedParsingSnapshotAndIntent(t *testing.T) {
	skill := skillInput(t, "private-skill-canary")
	raw := append(append([]byte(`{"skills":[`), skill...), []byte(`]}`)...)
	template, err := decodeTemplateInput(raw)
	if err != nil || !template.SetSkills || len(template.Initialization.Skills) != 1 {
		t.Fatal("template", err)
	}
	environment := append([]byte(`{"type":"openai_hosted",`), raw[1:]...)
	decode := func(agent string, environment []byte) sessionRequest {
		var request decodedSessionRequest
		if err := json.Unmarshal(append(append([]byte(`{`+agent+`,"environment":`), environment...), '}'), &request); err != nil {
			t.Fatal(err)
		}
		input, err := request.validated()
		if err != nil {
			t.Fatal(err)
		}
		return input
	}
	inline := decode(`"agent":{"model":"test"}`, environment)
	if !bytes.Equal(inline.initialization.Skills[0].Archive, template.Initialization.Skills[0].Archive) {
		t.Fatal("inline/template differ")
	}
	configuration, err := resolve(inline, "tenant", "key", nil)
	if err != nil || bytes.Contains(configuration, []byte(`"source"`)) || bytes.Contains(configuration, []byte(`"archive"`)) {
		t.Fatal("confidential snapshot", err)
	}
	public, err := storedEnvironment(mustEnvironment(t, configuration))
	if err != nil || len(public.Skills) != 1 || !bytes.Contains(public.Skills[0], []byte(`"name":"proof"`)) {
		t.Fatal("metadata", err)
	}
	intent, err := sessionCreationRequest(decode(`"agent_id":"saved"`, environment), nil)
	if err != nil {
		t.Fatal(err)
	}
	changed := append(append([]byte(`{"type":"openai_hosted","skills":[`), skillInput(t, "different")...), []byte(`]}`)...)
	other, err := sessionCreationRequest(decode(`"agent_id":"saved"`, changed), nil)
	if err != nil || bytes.Equal(intent, other) {
		t.Fatal("archive omitted from retry identity", err)
	}
	for _, clearing := range []string{`{"skills":null}`, `{"skills":[]}`} {
		input, err := decodeTemplateInput([]byte(clearing))
		if err != nil || !input.SetSkills || !input.Initialization.Empty() {
			t.Fatal("clear", err)
		}
	}
	for _, invalid := range []string{`{"skills":[null]}`, `{"skills":[{"type":"skill_reference","skill_id":"foreign"}]}`, `{"skills":[{"type":"inline","name":"proof","description":"A proof.","source":{"type":"base64","media_type":"application/zip","data":"invalid"}}]}`} {
		if _, err := decodeTemplateInput([]byte(invalid)); err == nil {
			t.Fatal("invalid or unsupported skill accepted")
		}
	}
	duplicate := append(append(append(append([]byte(`{"skills":[`), skill...), ','), skill...), []byte(`]}`)...)
	if _, err := decodeTemplateInput(duplicate); err == nil {
		t.Fatal("duplicate skill accepted")
	}
}

func mustEnvironment(t *testing.T, configuration []byte) json.RawMessage {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(configuration, &fields); err != nil {
		t.Fatal(err)
	}
	return fields["environment"]
}
