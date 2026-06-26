// Package parser converts user-pasted capability source text into a
// canonical.Spec. Split per kind (mcp_parser.go / skill_parser.go) because
// each carries different format tolerance — MCP accepts three competing
// vendor shapes (Claude Code JSON, OpenCode JSON, Codex TOML); Skill
// accepts markdown-with-frontmatter only.
//
// Hard invariants:
//   - Parsers NEVER inspect env values to guess what's a secret. Every
//     value comes back as EnvModeLiteral; the user marks secrets in the UI.
//   - Parsers are pure (no DB, no network). State-aware checks live in
//     the commit handler.
//   - Parse warnings are non-fatal — they ship to the preview UI.
package parser

import "errors"

// SourceFormat is the wire-level format of the pasted raw_text.
type SourceFormat string

const (
	SourceFormatJSON     SourceFormat = "json"
	SourceFormatTOML     SourceFormat = "toml"
	SourceFormatMarkdown SourceFormat = "markdown"
	// SourceFormatZip: bytes don't travel in raw_text; the client presigns +
	// PUTs to OSS and references the upload by oss_key. Used by Plugin and Skill.
	SourceFormatZip SourceFormat = "zip"
)

// ErrEmptyInput is sentinel so the HTTP handler can map "user submitted
// whitespace" to a friendlier 400 than a JSON-decode error.
var ErrEmptyInput = errors.New("parser: input is empty")

var ErrUnsupportedSourceFormat = errors.New("parser: unsupported source format for this kind")
