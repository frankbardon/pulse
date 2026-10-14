package descriptor

import (
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/internal/fieldaccept"
)

// narrowerDeclarations are the tabled operators whose manifest
// accepts_types is a strict SUBSET of the table: the runtime does not
// refuse the extra types (so the table, which mirrors the runtime's
// refusals, accepts them), but the declaration recommends only the types
// the operator is meant for. A narrower declaration never promises a
// refused type; a wider one would.
var narrowerDeclarations = map[string]bool{
	"ATTR_NORMALIZED": true, "ATTR_PERCENTILE": true, "ATTR_TSCORE": true, "ATTR_ZSCORE": true,
	"ATTR_REG_FITTED": true, "ATTR_REG_RESIDUAL": true, "ATTR_REG_LEVERAGE": true,
	"GROUP_QUANTILE": true, "GROUP_RANGE": true, "GROUP_ROUNDED": true,
	"FILTER_RANGE": true,
}

// TestCapabilities_AcceptsTypesMatchFieldAcceptTable pins the manifest's
// accepts_types to the accepted-types table (internal/fieldaccept) for
// every tabled operator that declares them: the declaration is the rule
// predict and the runtime enforce, never a second list — or, for
// narrowerDeclarations, a subset of it.
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
		slot := fieldaccept.SlotField
		if _, tabled := fieldaccept.Lookup(op, slot); !tabled {
			slot = fieldaccept.SlotPredictor
		}
		want := fieldaccept.TypeNames(op, slot)
		checked++
		if !narrowerDeclarations[op] {
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s accepts_types %v, table %v", op, got, want)
			}
			continue
		}
		table := map[string]bool{}
		for _, n := range want {
			table[n] = true
		}
		for _, n := range got {
			if !table[n] {
				t.Errorf("%s declares %s, which the table refuses", op, n)
			}
		}
		if reflect.DeepEqual(got, want) {
			t.Errorf("%s declares the whole table; drop it from narrowerDeclarations", op)
		}
	}
	if want := 12 + 19; checked != want {
		t.Errorf("checked %d tabled operators with a declaration, want %d (8 windows, FEAT_POLY, 3 regressions, 19 value readers)", checked, want)
	}
}
