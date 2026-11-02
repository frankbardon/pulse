package processing

import (
	"encoding/json"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// U64 #644: every opt-in test option has a weighted form under the one
// rule (the frequency formula on w*), so under a frequency weight each
// equals the unweighted option on the rep()-expanded rows: the pooled
// t, the exact Mann-Whitney p (the expansion's ties included), Yates'
// χ² and prop.test's continuity correction and Wilson interval.

// optionFigures compares a weighted result with its expansion's:
// statistic, df, p, variant and the named details at relative 1e-12.
func optionFigures(t *testing.T, name string, got, want *types.TestResult, details ...string) {
	t.Helper()
	if got.Variant != want.Variant {
		t.Fatalf("%s: variant %q, expansion %q", name, got.Variant, want.Variant)
	}
	for _, f := range []struct {
		what      string
		got, want float64
	}{{"statistic", got.Statistic, want.Statistic}, {"df", got.DF, want.DF}, {"p", got.PValue, want.PValue}} {
		if !relEq(f.got, f.want, 1e-12) {
			t.Errorf("%s: %s %.17g, expansion %.17g", name, f.what, f.got, f.want)
		}
	}
	for _, k := range details {
		g, gok := got.Details[k].(float64)
		w, wok := want.Details[k].(float64)
		if !gok || !wok || !relEq(g, w, 1e-12) {
			t.Errorf("%s: %s %v, expansion %v", name, k, got.Details[k], want.Details[k])
		}
	}
}

func TestTestOptions_FrequencyWeightIsExpansion(t *testing.T) {
	t.Parallel()

	rows := onlyGroups(weightedTestFixture(), "a", "b")
	for _, c := range []struct {
		name    string
		spec    types.Test
		details []string
	}{
		{"t pooled", types.Test{Type: types.TEST_T, Field: "x", SplitBy: "g", Params: json.RawMessage(`{"variance":"pooled"}`)}, []string{"ci_low", "ci_high", "pooled_variance", "diff"}},
		{"t pooled cuped", types.Test{Type: types.TEST_T, Field: "x", SplitBy: "g", Params: json.RawMessage(`{"variance":"pooled","covariate":"y"}`)}, []string{"ci_low", "ci_high", "pooled_variance", "variance_reduction"}},
		{"mann-whitney exact", types.Test{Type: types.TEST_MANN_WHITNEY_U, Field: "x", SplitBy: "g", Params: json.RawMessage(`{"exact":true}`)}, []string{"u_a"}},
	} {
		got := runWeightedRowTest(t, c.spec, rows, types.WeightKindFrequency)
		want := runWeightedRowTest(t, c.spec, expandRows(rows), "")
		optionFigures(t, c.name, got, want, c.details...)
	}
	if got := runWeightedRowTest(t, types.Test{Type: types.TEST_MANN_WHITNEY_U, Field: "x", SplitBy: "g", Params: json.RawMessage(`{"exact":true}`)}, rows, types.WeightKindFrequency); got.Variant != "exact" {
		t.Fatalf("weighted exact ran %q", got.Variant)
	}

	// The contingency options on a two-categorical table: (r, c, w).
	type cell struct {
		r, c string
		w    float64
	}
	table := []cell{{"yes", "a", 3}, {"no", "a", 2}, {"yes", "a", 4}, {"no", "a", 1}, {"yes", "b", 1}, {"no", "b", 5}, {"no", "b", 2}, {"yes", "b", 2}}
	run := func(spec types.Test, cells []cell, kind types.WeightKind) *types.TestResult {
		schema := &encoding.Schema{Fields: []encoding.Field{
			{Name: "r", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
			{Name: "c", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
			{Name: "w", Type: encoding.FieldTypeF64},
		}}
		dictIDOrAdd(schema, "r", "yes")
		dictIDOrAdd(schema, "r", "no")
		if kind != "" {
			spec.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: kind})
		}
		return runRegisteredRow(t, &spec, schema, func(rt RowTest) {
			for _, e := range cells {
				rec := NewRecord(schema, map[string]float64{"r": float64(dictIDOrAdd(schema, "r", e.r)), "c": float64(dictIDOrAdd(schema, "c", e.c)), "w": e.w})
				if err := rt.UpdateRow(rec); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
	var expanded []cell
	for _, e := range table {
		for range int(e.w) {
			expanded = append(expanded, cell{e.r, e.c, 1})
		}
	}
	for _, c := range []struct {
		name    string
		spec    types.Test
		details []string
	}{
		{"chisq yates", types.Test{Type: types.TEST_CHISQ, Rows: "r", Cols: "c", Params: json.RawMessage(`{"correct":true}`)}, []string{"continuity_correction"}},
		{"prop two correct", types.Test{Type: types.TEST_PROP_Z, Field: "r", SplitBy: "c", Params: json.RawMessage(`{"success":"yes","correct":true}`)}, []string{"ci_low", "ci_high", "continuity_correction"}},
		{"prop one wilson correct", types.Test{Type: types.TEST_PROP_Z, Field: "r", Params: json.RawMessage(`{"success":"yes","p0":0.4,"interval":"wilson","correct":true}`)}, []string{"ci_low", "ci_high", "continuity_correction"}},
		{"prop one wald correct", types.Test{Type: types.TEST_PROP_Z, Field: "r", Params: json.RawMessage(`{"success":"yes","p0":0.4,"correct":true}`)}, []string{"ci_low", "ci_high"}},
	} {
		got := run(c.spec, table, types.WeightKindFrequency)
		want := run(c.spec, expanded, "")
		optionFigures(t, c.name, got, want, c.details...)
	}
}
