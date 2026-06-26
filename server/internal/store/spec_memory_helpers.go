package store

import (
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

// optionalUUID is the error-returning sibling of nullableUUID: empty
// (after trim) maps to SQL NULL; anything else must parse as a UUID.
// errLabel is woven into the error so callers see which FK column failed.
func optionalUUID(value, errLabel string) (pgtype.UUID, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return pgtype.UUID{}, nil
	}
	id, err := uuid(v)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("%s: %w", errLabel, err)
	}
	return id, nil
}

// normalizeTags returns a non-nil slice: pgx encodes a Go nil as SQL
// NULL, which would violate the NOT NULL on spec_fragments.tags and
// memories.tags.
func normalizeTags(tags []string) []string {
	if tags == nil {
		return []string{}
	}
	return tags
}
