package skillcatalogdata

import "embed"

// FS contains the catalog metadata and the pinned, reviewed Skill packages.
// The server never fetches arbitrary source URLs during an import.
//
//go:embed catalog.json items
var FS embed.FS

//go:embed catalog.json
var CatalogJSON []byte
