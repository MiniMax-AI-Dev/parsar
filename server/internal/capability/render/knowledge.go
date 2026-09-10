package render

import (
	"encoding/json"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
)

func renderKnowledge(spec *canonical.KnowledgeSpec) (Output, error) {
	content, err := json.Marshal(spec)
	return Output{Content: content}, err
}
