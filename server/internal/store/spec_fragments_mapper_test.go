package store

import (
	"reflect"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

func TestSpecFragmentRowMappers(t *testing.T) {
	row := specFragmentRow{ID: "fragment-id", WorkspaceID: "workspace-id", Tags: []string{"tag"}}
	expected := specFragmentFromRow(row)
	for name, actual := range map[string]SpecFragmentRead{
		"insert": specFragmentFromInsertRow(sqlc.InsertSpecFragmentRow(row)), "get": specFragmentFromGetRow(sqlc.GetSpecFragmentRow(row)),
		"list": specFragmentFromListRow(sqlc.ListWorkspaceSpecFragmentsRow(row)), "list since": specFragmentFromListSinceRow(sqlc.ListWorkspaceSpecFragmentsSinceRow(row)),
		"update": specFragmentFromUpdateRow(sqlc.UpdateSpecFragmentRow(row)),
	} {
		t.Run(name, func(t *testing.T) {
			if !reflect.DeepEqual(actual, expected) {
				t.Errorf("mapper result = %#v, want %#v", actual, expected)
			}
		})
	}
}
