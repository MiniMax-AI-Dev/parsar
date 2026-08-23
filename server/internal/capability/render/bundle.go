package render

import (
	"encoding/json"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
)

// bundleDocument is the wire shape emitted by every scaffold's renderer
// for KindBundle. Like systemPromptDocument, the daemon never consumes
// it — the connector-side resolveBundleCapability reads the spec
// directly. The renderer call exists only so the renderer factory's
// Supports() returns true and the default switch doesn't reject the kind.
type bundleDocument struct {
	Name    string   `json:"name"`
	Version string   `json:"version"`
	Skills  []string `json:"skills,omitempty"`
	Tools   []string `json:"tools,omitempty"`
}

func renderBundle(b *canonical.BundleSpec) (Output, error) {
	if b == nil {
		return Output{}, fmt.Errorf("render: nil bundle spec")
	}
	skills := make([]string, 0, len(b.Skills))
	for _, s := range b.Skills {
		skills = append(skills, s.Slug)
	}
	body, err := json.Marshal(bundleDocument{
		Name:    b.Name,
		Version: b.Version,
		Skills:  skills,
		Tools:   b.Tools,
	})
	if err != nil {
		return Output{}, fmt.Errorf("render: marshal bundle: %w", err)
	}
	return Output{Content: body}, nil
}
