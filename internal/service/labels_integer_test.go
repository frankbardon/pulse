package service

import (
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
)

// TestLabelKeyOf_NumbersUnderAugmentOnly: a bound cell's lookup key is a
// string as is under either mode; a whole non-negative number (an
// integer-coded field's cell) becomes its decimal spelling under augment
// only — a numeric binding is augment-only, so replace never reads one —
// and a null, a fraction or a negative is no key.
func TestLabelKeyOf_NumbersUnderAugmentOnly(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		v    any
		mode types.LabelMode
		want string
		ok   bool
	}{
		{"US", types.LabelModeReplace, "US", true},
		{"7", types.LabelModeAugment, "7", true},
		{7.0, types.LabelModeAugment, "7", true},
		{4000000000.0, types.LabelModeAugment, "4000000000", true},
		{uint64(15), types.LabelModeAugment, "15", true},
		{int64(3), types.LabelModeAugment, "3", true},
		{3, types.LabelModeAugment, "3", true},
		{7.0, types.LabelModeReplace, "", false},
		{7.5, types.LabelModeAugment, "", false},
		{-1.0, types.LabelModeAugment, "", false},
		{int64(-1), types.LabelModeAugment, "", false},
		{nil, types.LabelModeAugment, "", false},
	} {
		got, ok := labelKeyOf(tc.v, tc.mode)
		if got != tc.want || ok != tc.ok {
			t.Errorf("labelKeyOf(%v %T, %s) = %q, %v; want %q, %v", tc.v, tc.v, tc.mode, got, ok, tc.want, tc.ok)
		}
	}
}

// TestApplyLabelsToResponse_NumericCellAugment: a Response.Data cell
// holding an integer-coded field's NUMBER gets its augment sibling by the
// number's decimal spelling (null on a miss) and keeps the number; a null
// cell gets no sibling.
func TestApplyLabelsToResponse_NumericCellAugment(t *testing.T) {
	t.Parallel()
	bindings := []*types.LabelBinding{{Field: "code", Table: "codes", Mode: types.LabelModeAugment}}
	r, err := processing.BuildLabelResolver(bindings, &processing.ExtensionRegistry{LabelTables: map[string]processing.LabelTable{
		"codes": {Rows: map[string]string{"1": "One"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	resp := &types.Response{Data: []map[string]any{{"code": 1.0}, {"code": 9.0}, {"code": nil}}}
	applyLabelsToResponse(&types.Request{Labels: bindings}, resp, r, nil)
	want := []map[string]any{{"code": 1.0, "code_label": "One"}, {"code": 9.0, "code_label": nil}, {"code": nil}}
	if !reflect.DeepEqual(resp.Data, want) {
		t.Errorf("rows = %v, want %v", resp.Data, want)
	}
}
