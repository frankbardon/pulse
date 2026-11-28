package mcp

import (
	"testing"

	"github.com/frankbardon/pulse/encoding"
)

// TestBindSetFieldTypes (#749b): classifyFields' set bucket narrows the
// bound request schema — an AGG_SET_* / FILTER_SET_* / GROUP_SET_*
// item (top level and crosstab) validates only with a set field, every
// other operator keeps the cohort-wide enum, and a cohort with no set
// field binds the set operators' field `false`.
func TestBindSetFieldTypes(t *testing.T) {
	t.Parallel()

	withSets := &encoding.Schema{Fields: []encoding.Field{
		{Name: "tags", Type: encoding.FieldTypeSetU8, Dictionary: encoding.NewDictionary()},
		{Name: "wide", Type: encoding.FieldTypeSetU256, Dictionary: encoding.NewDictionary()},
		{Name: "spend", Type: encoding.FieldTypeF64},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
	}}
	raw, err := buildRequestSchemaWithExtensions(classifyFields(withSets), nil)
	if err != nil {
		t.Fatal(err)
	}
	sch := compileBound(t, raw)
	for _, tc := range []struct {
		name, req string
		ok        bool
	}{
		{"filter set on set", `{"filterers":[{"type":"FILTER_SET_CONTAINS_ANY","field":"tags","values":["a"]}]}`, true},
		{"filter set on wide set", `{"filterers":[{"type":"FILTER_SET_EQUALS","field":"wide","values":["a"]}]}`, true},
		{"filter set on number", `{"filterers":[{"type":"FILTER_SET_CONTAINS_ANY","field":"spend","values":["a"]}]}`, false},
		{"filter include on categorical", `{"filterers":[{"type":"FILTER_INCLUDE","field":"region","values":["a"]}]}`, true},
		{"agg set on categorical", `{"aggregations":[{"type":"AGG_SET_FREQUENCY","field":"region"}]}`, false},
		{"agg set on set", `{"aggregations":[{"type":"AGG_SET_FREQUENCY","field":"tags"}]}`, true},
		{"agg sum on number", `{"aggregations":[{"type":"AGG_SUM","field":"spend"}]}`, true},
		{"group set on number", `{"groups":[{"type":"GROUP_SET_VALUE","field":"spend"}],"aggregations":[{"type":"AGG_COUNT","field":"spend"}]}`, false},
		{"group set on set", `{"groups":[{"type":"GROUP_SET_PER_ELEMENT","field":"tags"}],"aggregations":[{"type":"AGG_COUNT","field":"spend"}]}`, true},
		{"crosstab row set on number", `{"crosstab":{"rows":[{"type":"GROUP_SET_VALUE","field":"spend"}],"cell":{"type":"AGG_COUNT","field":"spend"}}}`, false},
		{"crosstab column set on number", `{"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"region"}],"columns":[{"type":"GROUP_SET_VALUE","field":"spend"}],"cell":{"type":"AGG_COUNT","field":"spend"}}}`, false},
		{"crosstab cell set on number", `{"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"region"}],"cell":{"type":"AGG_SET_FREQUENCY","field":"spend"}}}`, false},
		{"crosstab cell set on set", `{"crosstab":{"rows":[{"type":"GROUP_CATEGORY","field":"region"}],"cell":{"type":"AGG_SET_FREQUENCY","field":"tags"}}}`, true},
	} {
		if err := validates(t, sch, tc.req); (err == nil) != tc.ok {
			t.Errorf("%s: validation error %v, want ok=%v", tc.name, err, tc.ok)
		}
	}

	noSets := &encoding.Schema{Fields: []encoding.Field{{Name: "spend", Type: encoding.FieldTypeF64}}}
	raw, err = buildRequestSchemaWithExtensions(classifyFields(noSets), nil)
	if err != nil {
		t.Fatal(err)
	}
	sch = compileBound(t, raw)
	if err := validates(t, sch, `{"filterers":[{"type":"FILTER_SET_CONTAINS_ANY","field":"spend","values":["a"]}]}`); err == nil {
		t.Error("a set filter validated on a cohort with no set field")
	}
	if err := validates(t, sch, `{"filterers":[{"type":"FILTER_INCLUDE","field":"spend","values":["a"]}]}`); err != nil {
		t.Errorf("FILTER_INCLUDE on the cohort's field: %v", err)
	}
}

// TestSetOnlyField: the set-only operators are exactly fieldaccept's
// set readers outside the attributes (which bind per type already).
func TestSetOnlyField(t *testing.T) {
	t.Parallel()

	for op, want := range map[string]bool{
		"FILTER_SET_CONTAINS_ANY": true, "FILTER_SET_EQUALS": true, "AGG_SET_FREQUENCY": true,
		"AGG_SET_UNION": true, "GROUP_SET_VALUE": true, "GROUP_SET_PER_ELEMENT": true,
		"FILTER_INCLUDE": false, "AGG_COUNT": false, "AGG_SUM": false, "GROUP_CATEGORY": false,
		"AGG_ACME_CUSTOM": false,
	} {
		if got := setOnlyField(op); got != want {
			t.Errorf("setOnlyField(%s) = %v, want %v", op, got, want)
		}
	}
}
