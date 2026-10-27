package pulse

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// fidRegistry is a registry of every table kind: two lookup tables (one
// function-driven), two reference tables and two range tables.
func fidRegistry() *processing.ExtensionRegistry {
	rows := make(map[string]float64, 200)
	for i := range 200 {
		rows[fmt.Sprintf("k%03d", i)] = float64(i) / 7
	}
	s, e := "2024-01-01", "2024-03-31"
	return &processing.ExtensionRegistry{
		LookupTables: map[string]processing.LookupTable{
			"big": {Rows: rows},
			"fn":  {Lookup: func(...string) (float64, bool, error) { return 0, true, nil }},
		},
		ReferenceVectors: vectors.NewReferenceTables(map[string]types.ReferenceVectorTable{
			"seg":   {Labels: []string{"a", "b"}, Profiles: map[string][]float64{"p": {1, 2}, "q": {3, 4}, "r": {5, 6}}},
			"other": {Labels: []string{"a", "b"}, Profiles: map[string][]float64{"p": {9, 9}}},
		}),
		RangeTables: map[string]processing.RangeTable{
			"win":   {Ranges: []processing.DateRangeSpec{{Label: "q1", Start: &s, End: &e}}},
			"spare": {Ranges: []processing.DateRangeSpec{{Label: "q1", Start: &s}}},
		},
		ExprFunctions: []processing.ExprFunction{{Name: "emb", Fn: func() float64 { return 1 }}},
	}
}

// TestPredicateDependencies: which tables a predicate keys on, and when
// it is opaque. Names come from literal lookup() / ref() arguments and
// FILTER_DATE_RANGES params.table; a non-literal name reads every table
// of its kind; repeats collapse; unregistered names add nothing.
func TestPredicateDependencies(t *testing.T) {
	t.Parallel()

	reg := fidRegistry()
	reg.Filterers = map[types.FiltererType]processing.FiltererFactory{"FILTER_FID_X": nil}
	win, _ := json.Marshal(map[string]string{"table": "win"})
	inline, _ := json.Marshal(map[string]any{"ranges": []map[string]string{{"label": "a", "start": "2024-01-01"}}})
	type ref struct{ kind, name string }
	cases := map[string]struct {
		expr   string
		direct []*types.Filterer
		want   []ref
		opaque bool
	}{
		"none":              {expr: `x > 1`},
		"lookup_literal":    {expr: `x > lookup("big", "k001") + lookup("big", "k002")`, want: []ref{{"lookup", "big"}}},
		"lookup_unknown":    {expr: `x > lookup("nope", "k")`},
		"lookup_function":   {expr: `x > lookup("fn", "k")`, opaque: true},
		"lookup_dynamic":    {expr: `x > lookup(t, "k")`, want: []ref{{"lookup", "big"}}, opaque: true},
		"ref_literal":       {expr: `dot(v, ref("seg", "p")) > 0`, want: []ref{{"reference", "seg"}}},
		"ref_dynamic":       {expr: `dot(v, ref(t, "p")) > 0`, want: []ref{{"reference", "other"}, {"reference", "seg"}}},
		"expr_function":     {expr: `x > emb()`, opaque: true},
		"range_table":       {direct: []*types.Filterer{{Type: types.FILTER_DATE_RANGES, Field: "d", Params: win}}, want: []ref{{"range", "win"}}},
		"range_inline":      {direct: []*types.Filterer{{Type: types.FILTER_DATE_RANGES, Field: "d", Params: inline}}},
		"extension":         {direct: []*types.Filterer{{Type: "FILTER_FID_X", Field: "x"}}, opaque: true},
		"builtin_direct":    {direct: []*types.Filterer{{Type: types.FILTER_TRUE}}},
		"mixed_sorted":      {expr: `x > lookup("big", "k")`, direct: []*types.Filterer{{Type: types.FILTER_DATE_RANGES, Field: "d", Params: win}}, want: []ref{{"lookup", "big"}, {"range", "win"}}},
		"unparseable":       {expr: `x > (`},
		"lookup_in_literal": {expr: `s == "lookup(\"big\")"`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			tables, opaque := predicateDependencies(reg, c.expr, c.direct)
			var got []ref
			for _, tb := range tables {
				if len(tb.SHA256) != 64 {
					t.Fatalf("%s/%s digest %q", tb.Kind, tb.Name, tb.SHA256)
				}
				got = append(got, ref{tb.Kind, tb.Name})
			}
			if !reflect.DeepEqual(got, c.want) || opaque != c.opaque {
				t.Fatalf("got %v opaque=%v, want %v opaque=%v", got, opaque, c.want, c.opaque)
			}
		})
	}
}

// TestPredicateDependencies_DigestDeterministic: a table's digest is a
// function of its content alone — repeated over fresh copies of the
// registry (fresh map iteration orders) it never moves, and each content
// change moves it.
func TestPredicateDependencies_DigestDeterministic(t *testing.T) {
	t.Parallel()

	win, _ := json.Marshal(map[string]string{"table": "win"})
	expr := `x > lookup("big", "k") && dot(v, ref("seg", "p")) > 0`
	direct := []*types.Filterer{{Type: types.FILTER_DATE_RANGES, Field: "d", Params: win}}
	first, _ := predicateDependencies(fidRegistry(), expr, direct)
	if len(first) != 3 {
		t.Fatalf("tables %v, want 3", first)
	}
	for range 50 {
		got, _ := predicateDependencies(fidRegistry(), expr, direct)
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("digests moved: %v vs %v", got, first)
		}
	}
	end := "2024-04-30"
	for name, mutate := range map[string]func(r *processing.ExtensionRegistry){
		"lookup_value": func(r *processing.ExtensionRegistry) { r.LookupTables["big"].Rows["k007"] = 99 },
		"lookup_key": func(r *processing.ExtensionRegistry) {
			r.LookupTables["big"].Rows["k999"] = 0
		},
		"reference_value": func(r *processing.ExtensionRegistry) { r.ReferenceVectors["seg"].Profiles["q"][1] = 4.5 },
		"range_bound": func(r *processing.ExtensionRegistry) {
			t := r.RangeTables["win"]
			t.Ranges = []processing.DateRangeSpec{{Label: "q1", Start: t.Ranges[0].Start, End: &end}}
			r.RangeTables["win"] = t
		},
	} {
		reg := fidRegistry()
		mutate(reg)
		got, _ := predicateDependencies(reg, expr, direct)
		if reflect.DeepEqual(got, first) {
			t.Fatalf("%s: a content change left the digests unmoved", name)
		}
	}
}
