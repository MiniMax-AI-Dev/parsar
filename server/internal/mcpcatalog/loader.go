package mcpcatalog

import (
	"fmt"
	"strings"

	mcpcatalogdata "github.com/MiniMax-AI-Dev/parsar/catalog/mcp"
)

type Options struct {
	BuiltinJSON []byte
}

type Loader struct {
	builtin    Catalog
	builtinErr error
}

func New(options Options) *Loader {
	builtinJSON := options.BuiltinJSON
	if len(builtinJSON) == 0 {
		builtinJSON = mcpcatalogdata.CatalogJSON
	}
	builtin, builtinErr := Decode(builtinJSON)
	return &Loader{builtin: builtin, builtinErr: builtinErr}
}

func (l *Loader) Load() (Catalog, error) {
	if l.builtinErr != nil {
		return Catalog{}, fmt.Errorf("load builtin catalog: %w", l.builtinErr)
	}
	return l.builtin, nil
}

func (c Catalog) Find(id string) (Item, bool) {
	id = strings.TrimSpace(id)
	for _, item := range c.Items {
		if item.ID == id {
			return item, true
		}
	}
	return Item{}, false
}
