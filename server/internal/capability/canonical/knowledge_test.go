package canonical

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestKnowledgeDocumentsValidation(t *testing.T) {
	valid := KnowledgeSpec{Documents: []KnowledgeDocument{{Name: "policy.md", Content: "入职第三天领取电脑。"}}}
	tests := map[string]KnowledgeSpec{
		"empty":     {},
		"blank":     {Documents: []KnowledgeDocument{{Name: "p", Content: " "}}},
		"duplicate": {Documents: append(append([]KnowledgeDocument{}, valid.Documents...), valid.Documents...)},
		"too large": {Documents: []KnowledgeDocument{{Name: "p", Content: strings.Repeat("x", MaxKnowledgeBytes)}}},
		"binary":    {Documents: []KnowledgeDocument{{Name: "p", Content: "x\x00y"}}},
	}
	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			if value.Validate() == nil {
				t.Fatal("invalid documents accepted")
			}
		})
	}
	spec := Spec{SchemaVersion: 1, Kind: KindKnowledge, Knowledge: &valid}
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Spec
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatal(err)
	}
	if decoded.Knowledge.Documents[0].Content != valid.Documents[0].Content {
		t.Fatal("content changed")
	}
	spec.Skill = &SkillSpec{Slug: "unexpected", Instruction: "unexpected"}
	if spec.Validate() == nil {
		t.Fatal("multiple bodies accepted")
	}
}
