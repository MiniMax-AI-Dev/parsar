package skillcatalog

import (
	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
)

const SchemaVersion = 1

type Catalog struct {
	SchemaVersion int    `json:"schema_version"`
	UpdatedAt     string `json:"updated_at"`
	Items         []Item `json:"items"`
}

type Item struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	Publisher     Publisher `json:"publisher"`
	IconURL       string    `json:"icon_url,omitempty"`
	HomepageURL   string    `json:"homepage_url,omitempty"`
	RepositoryURL string    `json:"repository_url,omitempty"`
	Verified      bool      `json:"verified"`
	Categories    []string  `json:"categories"`
	FeaturedRank  int       `json:"featured_rank"`
	Version       string    `json:"version"`
	License       string    `json:"license"`
	SourceRef     string    `json:"source_ref"`
	SourcePath    string    `json:"source_path"`
	ContentPath   string    `json:"content_path"`
}

type Publisher struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// Package is the server-authoritative representation used by both the detail
// endpoint and the existing capability import pipeline.
type Package struct {
	Spec   canonical.Spec
	Files  []canonical.SkillFile
	Zip    []byte
	SHA256 string
}
