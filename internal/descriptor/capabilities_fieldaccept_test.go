package descriptor

import (
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/internal/fieldaccept"
)

// TestCapabilities_AcceptsTypesMatchFieldAcceptTable pins the manifest's
// accepts_types to the accepted-types table (internal/fieldaccept) for
// every tabled operator that declares them: the declaration is the rule
// predict and the runtime enforce, never a second list.
func TestCapabilities_AcceptsTypesMatchFieldAcceptTable(t *testing.T) {
	declared := map[string][]string{}
	for _, c := range windowCapabilities() {
		declared[c.Name] = c.AcceptsTypes
	}
	for _, c := range featureCapabilities() {
		declared[c.Name] = c.AcceptsTypes
	}
	for _, c := range regressionCapabilities() {
		declared[c.Name] = c.AcceptsTypes
	}
	checked := 0
	for _, op := range fieldaccept.Operators() {
		got, ok := declared[op]
		if !ok {
			continue
		}
		slot := fieldaccept.SlotField
		if _, tabled := fieldaccept.Lookup(op, slot); !tabled {
			slot = fieldaccept.SlotPredictor
		}
		if want := fieldaccept.TypeNames(op, slot); !reflect.DeepEqual(got, want) {
			t.Errorf("%s accepts_types %v, table %v", op, got, want)
		}
		checked++
	}
	if checked != 12 {
		t.Errorf("checked %d tabled operators with a declaration, want 12 (8 windows, FEAT_POLY, 3 regressions)", checked)
	}
}
