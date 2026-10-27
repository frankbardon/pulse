package descriptor

import (
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/internal/fieldaccept"
)

// modeWiderDeclarations are the tabled operators whose declaration is a
// strict SUPERSET of the table because the row judges one mode only:
// FILTER_TRUE / FILTER_FALSE read a packed_bool in strict mode, and any
// scalar under values ["truthy"].
var modeWiderDeclarations = map[string]bool{"FILTER_TRUE": true, "FILTER_FALSE": true}

// TestCapabilities_AcceptsTypesMatchFieldAcceptTable pins the manifest's
// accepts_types to the accepted-types table (internal/fieldaccept) for
// every tabled operator that declares them: the declaration is the rule
// predict and the runtime enforce, never a second list. (There is no
// "narrower" allowance: a type the runtime reads correctly is declared,
// and one it would read wrongly is refused by the row.)
func TestCapabilities_AcceptsTypesMatchFieldAcceptTable(t *testing.T) {
	t.Parallel()

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
	for _, c := range aggregatorCapabilities() {
		declared[c.Name] = c.AcceptsTypes
	}
	for _, c := range attributeCapabilities() {
		declared[c.Name] = c.AcceptsTypes
	}
	for _, c := range grouperCapabilities() {
		declared[c.Name] = c.AcceptsTypes
	}
	for _, c := range filtererCapabilities() {
		declared[c.Name] = c.AcceptsTypes
	}
	checked := 0
	for _, op := range fieldaccept.Operators() {
		got, ok := declared[op]
		if !ok {
			continue
		}
		// accepts_types describes the primary field (a regression's
		// predictors); an operator tabled only on another slot
		// (FEAT_TRAIN_TEST_SPLIT's stratify, a contingency test's axes)
		// declares nothing the table judges.
		slot := fieldaccept.SlotField
		if _, tabled := fieldaccept.Lookup(op, slot); !tabled {
			slot = fieldaccept.SlotPredictor
		}
		if _, tabled := fieldaccept.Lookup(op, slot); !tabled {
			continue
		}
		want := fieldaccept.TypeNames(op, slot)
		checked++
		if modeWiderDeclarations[op] {
			declaredSet := map[string]bool{}
			for _, n := range got {
				declaredSet[n] = true
			}
			for _, n := range want {
				if !declaredSet[n] {
					t.Errorf("%s does not declare %s, which the table accepts", op, n)
				}
			}
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s accepts_types %v, table %v", op, got, want)
		}
	}
	if want := 17 + 19 + 14 + 3 + 1 + 2 + 10 + 19 + 1; checked != want {
		t.Errorf("checked %d tabled operators with a declaration, want %d (11 windows, FEAT_POLY, 5 regressions, 19 value readers, 14 set operators, GROUP_DATE + 2 date-range operators, GROUP_NUMERIC_RANGES, FILTER_TRUE / FILTER_FALSE, FEAT_DATE_FEATURES / ATTR_DATE_PART, ATTR_CODE_IN, 3 model-error aggregators + AGG_AUC, 3 categorical encoders, 16 numeric aggregators + FEAT_LOG / FEAT_SQRT / FEAT_BUCKETIZE, ATTR_PREVIOUS_STATE)", checked, want)
	}
}
