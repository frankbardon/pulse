package descriptor

import (
	"slices"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// wideSetDictionary builds a dictionary of n members ("m0".."m{n-1}").
// 206 is the motivating SPSS multi-response size: past 128 (so set_u256
// is the only rung that holds it) and past DefaultDictionaryLimit (so
// inspect must truncate it).
func wideSetDictionary(t *testing.T, n int) *encoding.Dictionary {
	t.Helper()
	d := encoding.NewDictionary()
	for i := range n {
		if _, err := d.Add("m" + strconv.Itoa(i)); err != nil {
			t.Fatalf("dict.Add(%d): %v", i, err)
		}
	}
	return d
}

// registeredFieldTypeNames walks encoding's FieldType registry the same
// way rawCohortFieldTypes does and returns every known type name.
func registeredFieldTypeNames(t *testing.T) []string {
	t.Helper()
	var out []string
	for i := range 256 {
		ft := encoding.FieldType(i)
		if !ft.IsKnown() {
			break
		}
		name := ft.String()
		if len(name) > 7 && name[:7] == "unknown" {
			continue
		}
		out = append(out, name)
	}
	return out
}

// TestDefaults_WideSetsMatchNarrow pins FR-21: a wide set column gets
// the same smart defaults a narrow one does, and Field.Nullable never
// changes the inferred operator.
func TestDefaults_WideSetsMatchNarrow(t *testing.T) {
	narrow, ok := defaultRules[encoding.FieldTypeSetU64]
	if !ok {
		t.Fatal("defaultRules has no set_u64 row to compare against")
	}
	for _, ft := range []encoding.FieldType{encoding.FieldTypeSetU128, encoding.FieldTypeSetU256} {
		rule, ok := defaultRules[ft]
		if !ok {
			t.Errorf("defaultRules has no %s row", ft)
			continue
		}
		if rule.Agg != narrow.Agg || rule.Group != narrow.Group {
			t.Errorf("%s rule = {%s, %s}, want the set_u64 pair {%s, %s}",
				ft, rule.Agg, rule.Group, narrow.Agg, narrow.Group)
		}
	}

	for _, nullable := range []bool{false, true} {
		schema := &encoding.Schema{Fields: []encoding.Field{
			{Name: "tags", Type: encoding.FieldTypeSetU256, Nullable: nullable,
				Dictionary: wideSetDictionary(t, 206), Description: "Multi-select survey tags"},
		}}
		req := &types.Request{
			Aggregations: []*types.Aggregation{{Field: "tags"}},
			Groups:       []*types.Group{{Field: "tags"}},
		}
		applied := ResolveDefaults(req, schema)
		if len(applied) != 2 {
			t.Fatalf("nullable=%v: ResolveDefaults applied %d defaults, want 2", nullable, len(applied))
		}
		if req.Aggregations[0].Type != narrow.Agg {
			t.Errorf("nullable=%v: aggregation default = %q, want %q", nullable, req.Aggregations[0].Type, narrow.Agg)
		}
		if req.Groups[0].Type != narrow.Group {
			t.Errorf("nullable=%v: grouper default = %q, want %q", nullable, req.Groups[0].Type, narrow.Group)
		}
	}

	// Explicit Type is never overridden.
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "tags", Type: encoding.FieldTypeSetU128, Dictionary: wideSetDictionary(t, 120),
			Description: "Multi-select survey tags"},
	}}
	req := &types.Request{Aggregations: []*types.Aggregation{{Field: "tags", Type: types.AGG_SET_UNION}}}
	if applied := ResolveDefaults(req, schema); len(applied) != 0 {
		t.Errorf("ResolveDefaults overrode an explicit Type: %+v", applied)
	}
}

// TestCapabilities_AllCohortFieldTypesCarriesEverySetRung keeps the
// "literal every field type" promise in allCohortFieldTypes' own doc
// comment honest for the set family: a registered rung missing from the
// list under-declares AGG_COUNT / AGG_NULL_COUNT / FILTER_NULL, which
// do handle set columns, and the under-declaration is silent.
//
// Deliberately scoped to set rungs. allCohortFieldTypes also carries
// five legacy `nullable_*` names encoding no longer registers and omits
// `u4`; that pre-existing drift is a separate change from this one and
// is not asserted here.
func TestCapabilities_AllCohortFieldTypesCarriesEverySetRung(t *testing.T) {
	for _, name := range registeredFieldTypeNames(t) {
		if len(name) < 4 || name[:4] != "set_" {
			continue
		}
		if !slices.Contains(allCohortFieldTypes, name) {
			t.Errorf("allCohortFieldTypes missing registered set rung %q", name)
		}
	}
}

// TestCapabilities_SetOperatorsDeclareEveryRung asserts no set rung is
// silently missing from an operator that declares the others. This is
// the declaration half of E2: the operators read every rung at runtime,
// so a manifest that stops at set_u64 tells a caller the opposite.
func TestCapabilities_SetOperatorsDeclareEveryRung(t *testing.T) {
	allRungs := []string{"set_u8", "set_u16", "set_u32", "set_u64", "set_u128", "set_u256"}
	groups := map[string][]Operator{
		"aggregator": aggregatorCapabilities(),
		"attribute":  attributeCapabilities(),
		"filterer":   filtererCapabilities(),
		"grouper":    grouperCapabilities(),
		"window":     windowCapabilities(),
		"feature":    featureCapabilities(),
	}
	for kind, ops := range groups {
		for _, op := range ops {
			declaresAny := false
			for _, rung := range allRungs {
				if slices.Contains(op.AcceptsTypes, rung) {
					declaresAny = true
					break
				}
			}
			if !declaresAny {
				continue
			}
			for _, rung := range allRungs {
				if !slices.Contains(op.AcceptsTypes, rung) {
					t.Errorf("%s %s declares some set rungs but not %q — partial set support is never the truth",
						kind, op.Name, rung)
				}
			}
		}
	}
}

// numericEchoOperators are the operators that read their field through
// Record.NumericValue, which refuses set columns outright. Declaring a
// set type on any of them is a promise the runtime cannot keep: before
// the refusal they matched the mask's lossy float echo, after it they
// would drop every row. Both are silent, so the declaration is the fix.
var numericEchoOperators = []string{
	string(types.FILTER_INCLUDE),
	string(types.FILTER_EXCLUDE),
	string(types.GROUP_CATEGORY),
	string(types.AGG_FREQUENCY),
	string(types.AGG_MODE),
	string(types.AGG_DISTINCT_COUNT),
}

func TestCapabilities_NumericEchoOperatorsDeclareNoSetType(t *testing.T) {
	var ops []Operator
	ops = append(ops, aggregatorCapabilities()...)
	ops = append(ops, filtererCapabilities()...)
	ops = append(ops, grouperCapabilities()...)

	seen := map[string]bool{}
	for _, op := range ops {
		if !slices.Contains(numericEchoOperators, op.Name) {
			continue
		}
		seen[op.Name] = true
		for _, tn := range op.AcceptsTypes {
			if len(tn) > 4 && tn[:4] == "set_" {
				t.Errorf("%s declares %q but reads the field's numeric echo; a set column would answer silently wrong",
					op.Name, tn)
			}
		}
	}
	for _, name := range numericEchoOperators {
		if !seen[name] {
			t.Errorf("capability tables carry no operator named %q", name)
		}
	}
}

// TestManifest_CohortTypesCarryWideSets checks the manifest's
// cohort_types block and its capability cross-references reach the new
// rungs, so an LLM bootstrapping from pulse_manifest sees a coherent
// 20-type world.
func TestManifest_CohortTypesCarryWideSets(t *testing.T) {
	m := BuildManifest()
	byName := map[string]CohortFieldType{}
	for _, ct := range m.CohortTypes {
		byName[ct.Name] = ct
	}
	for _, name := range []string{"set_u128", "set_u256"} {
		ct, ok := byName[name]
		if !ok {
			t.Fatalf("manifest cohort_types missing %q", name)
		}
		if !slices.Contains(ct.CompatibleAggregators, string(types.AGG_SET_UNION)) {
			t.Errorf("%s CompatibleAggregators missing AGG_SET_UNION: %v", name, ct.CompatibleAggregators)
		}
		if !slices.Contains(ct.CompatibleFilterers, string(types.FILTER_SET_CONTAINS_ANY)) {
			t.Errorf("%s CompatibleFilterers missing FILTER_SET_CONTAINS_ANY: %v", name, ct.CompatibleFilterers)
		}
		if !slices.Contains(ct.CompatibleGroupers, string(types.GROUP_SET_VALUE)) {
			t.Errorf("%s CompatibleGroupers missing GROUP_SET_VALUE: %v", name, ct.CompatibleGroupers)
		}
		if !slices.Contains(ct.CompatibleAttributes, string(types.ATTR_SET_POPCOUNT)) {
			t.Errorf("%s CompatibleAttributes missing ATTR_SET_POPCOUNT: %v", name, ct.CompatibleAttributes)
		}
		if slices.Contains(ct.CompatibleGroupers, string(types.GROUP_CATEGORY)) {
			t.Errorf("%s CompatibleGroupers still advertises GROUP_CATEGORY", name)
		}
	}
	// Narrow rungs must keep the same shape — the wide rungs are not a
	// separate world.
	narrow := byName["set_u64"]
	if !slices.Equal(narrow.CompatibleAggregators, byName["set_u256"].CompatibleAggregators) {
		t.Errorf("set_u64 and set_u256 disagree on CompatibleAggregators:\n%v\n%v",
			narrow.CompatibleAggregators, byName["set_u256"].CompatibleAggregators)
	}
}

// TestInspect_WideSetDictionaryTruncates pins FR-22: a 206-member
// set_u256 dictionary renders through the HasDictionary() gate and
// truncates at DefaultDictionaryLimit unless FullDict is set.
func TestInspect_WideSetDictionaryTruncates(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "tags", Type: encoding.FieldTypeSetU256, Dictionary: wideSetDictionary(t, 206),
			Description: "Multi-select survey response tags"},
	}}
	data := buildTestPulseFile(t, schema)

	env := InspectFromBytes(data, nil)
	if len(env.Errors) != 0 {
		t.Fatalf("inspect errors: %v", env.Errors)
	}
	res, ok := env.Data.(*InspectResult)
	if !ok {
		t.Fatalf("env.Data is %T, want *InspectResult", env.Data)
	}
	if len(res.Fields) != 1 {
		t.Fatalf("inspect returned %d fields, want 1", len(res.Fields))
	}
	f := res.Fields[0]
	if f.Type != "set_u256" {
		t.Errorf("field type = %q, want set_u256", f.Type)
	}
	if f.Dictionary == nil {
		t.Fatal("set_u256 field rendered no dictionary — the HasDictionary() gate dropped it")
	}
	if f.Dictionary.TotalEntries != 206 {
		t.Errorf("TotalEntries = %d, want 206", f.Dictionary.TotalEntries)
	}
	if !f.Dictionary.Truncated {
		t.Error("Truncated = false for a 206-member dictionary at the default limit")
	}
	if len(f.Dictionary.Values) != DefaultDictionaryLimit {
		t.Errorf("rendered %d values, want DefaultDictionaryLimit (%d)", len(f.Dictionary.Values), DefaultDictionaryLimit)
	}

	full := InspectFromBytes(data, &InspectOptions{FullDict: true})
	fullRes := full.Data.(*InspectResult)
	fd := fullRes.Fields[0].Dictionary
	if fd == nil || fd.Truncated || len(fd.Values) != 206 {
		t.Fatalf("FullDict inspect = %+v, want all 206 values untruncated", fd)
	}
}

// TestFacet_WideSetRejectsNumericStats pins FR-23: facet treats a wide
// set exactly like a narrow one, refusing numeric percentiles and
// histograms with SERVICE_VALIDATION rather than returning gibberish.
func TestFacet_WideSetRejectsNumericStats(t *testing.T) {
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "tags", Type: encoding.FieldTypeSetU256, Dictionary: wideSetDictionary(t, 206),
			Description: "Multi-select survey response tags"},
	}}
	data := buildTestPulseFile(t, schema)

	cases := []struct {
		name string
		req  *types.FacetRequest
	}{
		{"percentiles", &types.FacetRequest{Fields: []string{"tags"}, NumericPercentiles: []float64{0.5}}},
		{"histogram", &types.FacetRequest{Fields: []string{"tags"}, IncludeHistogram: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := ValidateFacetFromBytes(data, tc.req)
			found := false
			for _, e := range env.Errors {
				if e.Code == "SERVICE_VALIDATION" {
					found = true
				}
			}
			if !found {
				t.Errorf("no SERVICE_VALIDATION error for %s on a set_u256 field; errors=%v", tc.name, env.Errors)
			}
		})
	}
}
