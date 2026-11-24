package crosstabaxis

import (
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// TestNilGrouperRefusals pins the shared null-axis-grouper rule: one
// PROCESSING_CONFIG refusal per null slot, rows before columns, each
// naming its axis and index; a clean spec, an empty axis and a nil spec
// raise nothing.
func TestNilGrouperRefusals(t *testing.T) {
	t.Parallel()

	g := &types.Group{Type: types.GROUP_CATEGORY, Field: "region"}
	if got := NilGrouperRefusals(nil); got != nil {
		t.Errorf("nil spec: %v", got)
	}
	if got := NilGrouperRefusals(&types.CrosstabSpec{Rows: []*types.Group{g}}); got != nil {
		t.Errorf("clean spec, implicit Total column: %v", got)
	}
	got := NilGrouperRefusals(&types.CrosstabSpec{Rows: []*types.Group{g, nil}, Columns: []*types.Group{nil, g, nil}})
	want := []map[string]any{
		{"axis": "rows", "index": 1},
		{"axis": "columns", "index": 0},
		{"axis": "columns", "index": 2},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d refusals, want %d: %v", len(got), len(want), got)
	}
	for i, ce := range got {
		if ce.Code != errors.PROCESSING_CONFIG {
			t.Errorf("refusal %d: code %s, want PROCESSING_CONFIG", i, ce.Code)
		}
		if !reflect.DeepEqual(ce.Details, want[i]) {
			t.Errorf("refusal %d: details %v, want %v", i, ce.Details, want[i])
		}
	}
	if got[0].Message != "crosstab rows contains a null grouper at index 1" {
		t.Errorf("message = %q", got[0].Message)
	}
}
