package mcpcatalogdata

import _ "embed"

//go:embed catalog.json
var CatalogJSON []byte

//go:embed catalog.schema.json
var CatalogSchemaJSON []byte
