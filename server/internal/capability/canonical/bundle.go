package canonical

import (
	"fmt"
	"strings"
)

// BundleSpec is the body for Spec{Kind: KindBundle} — a Plugin Bundle that
// packages server tools, client UI, skills, and hooks as a single deployable
// unit. Installed via `parsarctl plugin add`, the bundle lives on the local
// filesystem under the server's plugins/ directory.
type BundleSpec struct {
	// Name is the unique plugin identifier, e.g. "@internal/jira-integration".
	Name string `json:"name"`

	// Version follows semver, e.g. "1.2.0".
	Version string `json:"version"`

	// Description is a human-readable summary for the admin UI.
	Description string `json:"description,omitempty"`

	// Author identifies the FDE team or individual who built this plugin.
	Author string `json:"author,omitempty"`

	// ServerEntry is the relative path to the Node.js server entry point,
	// e.g. "./server/index.js". Empty means the plugin has no server component.
	ServerEntry string `json:"server_entry,omitempty"`

	// ClientEntry is the relative path to the built client bundle,
	// e.g. "./client/index.js". Empty means the plugin has no UI component.
	ClientEntry string `json:"client_entry,omitempty"`

	// Skills holds inline skill definitions. Each entry has a slug (identifier)
	// and instruction (markdown body). These are injected as system prompt
	// additions (append mode) when the bundle is bound to an agent.
	// The `parsarctl plugin add` command reads skill files from disk and
	// embeds the content here at install time.
	Skills []BundleSkill `json:"skills,omitempty"`

	// Tools lists tool names the server entry exposes via MCP. Informational
	// for the admin UI; the actual tool registration happens at runtime.
	Tools []string `json:"tools,omitempty"`

	// Hooks lists event hook names the server entry registers. Informational.
	Hooks []string `json:"hooks,omitempty"`

	// Credentials lists credential kind codes the plugin requires.
	// The admin fills these via the existing credential_ref flow.
	Credentials []string `json:"credentials,omitempty"`
}

// BundleSkill is one skill embedded in a BundleSpec. The content is read
// from the plugin's skills/ directory at install time and stored inline
// in the canonical_spec so the resolver needs no filesystem access.
type BundleSkill struct {
	// Slug is the short identifier for the skill, e.g. "jira-workflow".
	Slug string `json:"slug"`

	// Instruction is the raw markdown body of the skill.
	Instruction string `json:"instruction"`
}

// maxBundleNameLen limits the name field to a reasonable length.
const maxBundleNameLen = 256

// Validate enforces structural sanity. Pure: no DB / network access.
func (b BundleSpec) Validate() error {
	name := strings.TrimSpace(b.Name)
	if name == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidBundle)
	}
	if len(name) > maxBundleNameLen {
		return fmt.Errorf("%w: name is too long (%d bytes, max %d)", ErrInvalidBundle, len(name), maxBundleNameLen)
	}
	if strings.TrimSpace(b.Version) == "" {
		return fmt.Errorf("%w: version is required", ErrInvalidBundle)
	}
	// At least one of server, client, or skills must be present — otherwise
	// the bundle does nothing.
	hasServer := strings.TrimSpace(b.ServerEntry) != ""
	hasClient := strings.TrimSpace(b.ClientEntry) != ""
	hasSkills := len(b.Skills) > 0
	if !hasServer && !hasClient && !hasSkills {
		return fmt.Errorf("%w: at least one of server_entry, client_entry, or skills must be set", ErrInvalidBundle)
	}
	for i, skill := range b.Skills {
		if strings.TrimSpace(skill.Slug) == "" {
			return fmt.Errorf("%w: skills[%d].slug is required", ErrInvalidBundle, i)
		}
		if strings.TrimSpace(skill.Instruction) == "" {
			return fmt.Errorf("%w: skills[%d].instruction is required", ErrInvalidBundle, i)
		}
	}
	return nil
}
