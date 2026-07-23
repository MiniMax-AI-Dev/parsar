package mcpcatalog

import (
	"context"
	"fmt"
	"strings"

	mcpcatalogdata "github.com/MiniMax-AI-Dev/parsar/catalog/mcp"
)

type Source string

const (
	SourceBuiltin Source = "builtin"
)

type Snapshot struct {
	Catalog Catalog
	Source  Source
}

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

	return &Loader{
		builtin:    builtin,
		builtinErr: builtinErr,
	}
}

func (l *Loader) Load(_ context.Context) (Snapshot, error) {
	if l.builtinErr != nil {
		return Snapshot{}, fmt.Errorf("load builtin catalog: %w", l.builtinErr)
	}
	return Snapshot{Catalog: l.builtin, Source: SourceBuiltin}, nil
}

func (s Snapshot) Find(id string) (Item, bool) {
	id = strings.TrimSpace(id)
	for _, item := range s.Catalog.Items {
		if item.ID == id {
			return item, true
		}
	}
	return Item{}, false
}
