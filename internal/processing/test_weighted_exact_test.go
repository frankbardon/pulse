package processing

import (
	"encoding/json"
	"math"
	"slices"
	"sort"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// Frequency-weighted Fisher exact, KS and Brown-Forsythe (E2-S2): each
// is ClassFrequencyOnly and equals the unweighted test on the
// physically expanded rows. Oracles as in test_weighted_rank_test.go:
// unity (w ≡ 1 byte-identical once sum_weights is shed), expansion
// (every figure to 1e-10 relative, raw counts aside) and exclusion of
// invalid / zero weights. The external pins (R fisher.test / ks.test /
// anova(lm(|x − median|))) live in internal/service's generated
// reference values.

func spreadTestCases() []weightedTestCase {
	all := rankFixture()
	two := onlyGroups(all, "a", "b")
	return []weightedTestCase{
		{"ks", types.Test{Type: types.TEST_KS, Field: "x", SplitBy: "g"}, two},
		{"brown_forsythe", types.Test{Type: types.TEST_BROWN_FORSYTHE, Field: "x", SplitBy: "g"}, all},
		// Unrounded: no ties inside a group, so the median and |x − m|
		// read fractional values.
		{"brown_forsythe_raw", types.Test{Type: types.TEST_BROWN_FORSYTHE, Field: "x", SplitBy: "g"}, weightedTestFixture()},
		{"ks_raw", types.Test{Type: types.TEST_KS, Field: "x", SplitBy: "g"}, onlyGroups(weightedTestFixture(), "a", "c")},
	}
}

// fisherRows is countFixture restricted to a 2×2 table (o ∈ {yes, no}).
func fisherRows() []cRow {
	var out []cRow
	for _, r := range countFixture() {
		if r.o != "maybe" {
			out = append(out, r)
		}
	}
	return out
}

var fisherSpec = types.Test{Type: types.TEST_FISHER_EXACT, Rows: "g", Cols: "o"}

// compareDetails checks every non-raw numeric detail of got against want
// to 1e-10 relative.
func compareDetails(t *testing.T, name string, got, want map[string]any, raw map[string]bool) {
	t.Helper()
	for k, wv := range want {
		if raw[k] {
			continue
		}
		if k == "contingency" {
			g, w := got[k].([][]float64), wv.([][]int)
			for i := range w {
				for j := range w[i] {
					if g[i][j] != float64(w[i][j]) {
						t.Fatalf("%s: contingency %v, want %v", name, g, w)
					}
				}
			}
			continue
		}
		wn, gn := numbersAny(wv), numbersAny(got[k])
		if wn == nil {
			if _, isStr := wv.([]string); !isStr {
				t.Fatalf("%s: detail %s (%T) not compared", name, k, wv)
			}
			if !slices.Equal(wv.([]string), got[k].([]string)) {
				t.Fatalf("%s: %s = %v, want %v", name, k, got[k], wv)
			}
			continue
		}
		if len(wn) != len(gn) {
			t.Fatalf("%s: %s shape %v vs %v", name, k, gn, wn)
		}
		for i := range wn {
			if !relEq(gn[i], wn[i], 1e-10) {
				t.Fatalf("%s: %s[%d] = %v, want %v", name, k, i, gn[i], wn[i])
			}
		}
	}
}

// TestWeightedSpreadTests_UnityMatchesUnweighted: w ≡ 1 under kind
// frequency is the unweighted result byte for byte, sum_weights = n.
func TestWeightedSpreadTests_UnityMatchesUnweighted(t *testing.T) {
	for _, tc := range spreadTestCases() {
		ones := append([]wRow(nil), tc.rows...)
		for i := range ones {
			ones[i].w = 1
		}
		base, _ := json.Marshal(runWeightedRowTest(t, tc.spec, tc.rows, ""))
		got := runWeightedRowTest(t, tc.spec, ones, types.WeightKindFrequency)
		if sw, n := numbersAny(got.Details["sum_weights"]), numbersAny(got.Details["n"]); sw == nil || !slices.Equal(sw, n) {
			t.Fatalf("%s: unity sum_weights %v, want n %v", tc.name, sw, n)
		}
		if got.Details["n_eff"] != nil {
			t.Fatalf("%s: n_eff under frequency", tc.name)
		}
		if b := shedWeightKeys(got); string(b) != string(base) {
			t.Fatalf("%s unity differs:\n got %s\nwant %s", tc.name, b, base)
		}
	}
	ones := fisherRows()
	for i := range ones {
		ones[i].w = 1
	}
	base, _ := json.Marshal(runCountTest(t, fisherSpec, ones, ""))
	got := runCountTest(t, fisherSpec, ones, types.WeightKindFrequency)
	if got.Details["sum_weights"] != float64(len(ones)) || got.Details["n"] != len(ones) {
		t.Fatalf("fisher: unity sum_weights %v / n %v, want %d", got.Details["sum_weights"], got.Details["n"], len(ones))
	}
	// The weighted contingency is the Σw table as floats; at unity it
	// marshals exactly like the int counts.
	if b := shedWeightKeys(got); string(b) != string(base) {
		t.Fatalf("fisher unity differs:\n got %s\nwant %s", b, base)
	}
}

// TestWeightedSpreadTests_FrequencyIsExpansion: integer weights equal
// the expanded rows run unweighted.
func TestWeightedSpreadTests_FrequencyIsExpansion(t *testing.T) {
	raw := map[string]bool{"n": true, "sum_weights": true}
	for _, tc := range spreadTestCases() {
		got := runWeightedRowTest(t, tc.spec, tc.rows, types.WeightKindFrequency)
		want := runWeightedRowTest(t, tc.spec, expandRows(tc.rows), "")
		if !relEq(got.Statistic, want.Statistic, 1e-12) || !relEq(got.DF, want.DF, 1e-12) || !relEq(got.PValue, want.PValue, 1e-10) {
			t.Fatalf("%s: stat/df/p %v %v %v, want %v %v %v", tc.name, got.Statistic, got.DF, got.PValue, want.Statistic, want.DF, want.PValue)
		}
		if got.Details["n_eff"] != nil || !slices.Equal(got.Warnings, want.Warnings) {
			t.Fatalf("%s: n_eff %v / warnings %v, want %v", tc.name, got.Details["n_eff"], got.Warnings, want.Warnings)
		}
		if gs, wn := numbersAny(got.Details["sum_weights"]), numbersAny(want.Details["n"]); !slices.Equal(gs, wn) {
			t.Fatalf("%s: sum_weights %v, want expanded n %v", tc.name, gs, wn)
		}
		if gn := numbersAny(got.Details["n"]); sum(gn) != float64(len(tc.rows)) {
			t.Fatalf("%s: raw n %v, want %d rows", tc.name, gn, len(tc.rows))
		}
		compareDetails(t, tc.name, got.Details, want.Details, raw)
	}
	rows := fisherRows()
	var expanded []cRow
	for _, r := range rows {
		for range int(r.w) {
			expanded = append(expanded, r)
		}
	}
	got := runCountTest(t, fisherSpec, rows, types.WeightKindFrequency)
	want := runCountTest(t, fisherSpec, expanded, "")
	if !relEq(got.Statistic, want.Statistic, 1e-12) || !relEq(got.PValue, want.PValue, 1e-10) {
		t.Fatalf("fisher: stat/p %v %v, want %v %v", got.Statistic, got.PValue, want.Statistic, want.PValue)
	}
	if got.Details["sum_weights"] != float64(want.Details["n"].(int)) || got.Details["n"] != len(rows) {
		t.Fatalf("fisher: sum_weights %v / n %v, want %v / %d", got.Details["sum_weights"], got.Details["n"], want.Details["n"], len(rows))
	}
	compareDetails(t, "fisher", got.Details, want.Details, raw)
}

// TestWeightedSpreadTests_ExcludeInvalidAndZero: a null, negative, NaN,
// infinite, non-integer or zero weight drops the row.
func TestWeightedSpreadTests_ExcludeInvalidAndZero(t *testing.T) {
	schema := weightedTestSchema()
	for _, tc := range spreadTestCases() {
		spec := tc.spec
		spec.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})
		rt, err := rowTestRegistry[spec.Type](&spec, schema)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range tc.rows {
			_ = rt.UpdateRow(NewRecord(schema, map[string]float64{"x": r.x, "y": r.y, "w": r.w, "g": float64(dictIDOrAdd(schema, "g", r.g))}))
		}
		g := float64(dictIDOrAdd(schema, "g", tc.rows[0].g))
		for _, bad := range []map[string]float64{
			{"x": 99, "g": g},
			{"x": 99, "w": -2, "g": g},
			{"x": 99, "w": math.NaN(), "g": g},
			{"x": 99, "w": math.Inf(1), "g": g},
			{"x": 99, "w": 0, "g": g},
			{"x": 99, "w": 2.5, "g": g},
		} {
			_ = rt.UpdateRow(NewRecord(schema, bad))
		}
		got, err := rt.Finalize()
		if err != nil {
			t.Fatal(err)
		}
		want := runWeightedRowTest(t, tc.spec, tc.rows, types.WeightKindFrequency)
		if a, b := shedWeightKeys(got), shedWeightKeys(want); string(a) != string(b) {
			t.Fatalf("%s: invalid rows leaked:\n got %s\nwant %s", tc.name, a, b)
		}
	}
	// Fisher: a zero-weight row of a NEW level must not register the
	// level (the expansion never sees it), nor reorder the table.
	cs := countSchema()
	spec := fisherSpec
	spec.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})
	rt, err := rowTestRegistry[spec.Type](&spec, cs)
	if err != nil {
		t.Fatal(err)
	}
	_ = rt.UpdateRow(countRecord(cs, cRow{g: "a", o: "maybe", w: 0}))
	_ = rt.UpdateRow(countRecord(cs, cRow{g: "b", o: "no", w: 2.5}))
	for _, r := range fisherRows() {
		_ = rt.UpdateRow(countRecord(cs, r))
	}
	got, err := rt.Finalize()
	if err != nil {
		t.Fatalf("fisher: excluded rows leaked into the table: %v", err)
	}
	want := runCountTest(t, fisherSpec, fisherRows(), types.WeightKindFrequency)
	if a, b := shedWeightKeys(got), shedWeightKeys(want); string(a) != string(b) {
		t.Fatalf("fisher: invalid rows leaked:\n got %s\nwant %s", a, b)
	}
}

// TestKSTwoSampleD_Ties: D is read only after a tie run is consumed on
// both sides, so identical samples give 0 however their ties fall (the
// pre-weighting walk compared inside tie runs: 0.5 here), and a
// weighted ECDF equals the expanded one.
func TestKSTwoSampleD_Ties(t *testing.T) {
	if d := ksTwoSampleD([]float64{1, 1}, []float64{1, 1, 1, 1}); d != 0 {
		t.Fatalf("D over identical tied samples = %v, want 0", d)
	}
	if d := ksTwoSampleD([]float64{1, 2, 2, 3}, []float64{2, 2, 3, 3}); d != 0.25 {
		t.Fatalf("D = %v, want 0.25 (F_a(2)=0.75, F_b(2)=0.5)", d)
	}
	a, wa := []float64{1, 2, 4}, []float64{2, 1, 3}
	b, wb := []float64{2, 3, 4}, []float64{1, 4, 1}
	expand := func(vs, ws []float64) []float64 {
		var out []float64
		for i, v := range vs {
			for range int(ws[i]) {
				out = append(out, v)
			}
		}
		return out
	}
	if g, w := ksTwoSampleDW(a, wa, b, wb), ksTwoSampleD(expand(a, wa), expand(b, wb)); g != w {
		t.Fatalf("weighted D %v, expanded %v", g, w)
	}
	// NaN runs terminate.
	_ = ksTwoSampleD([]float64{math.NaN(), 1}, []float64{math.NaN(), 2})
}

// TestWeightedMedianSorted_EqualsExpansion: the frequency median is the
// median of the expanded rows; unit weights are median() bit for bit.
func TestWeightedMedianSorted_EqualsExpansion(t *testing.T) {
	for _, tc := range []struct{ vs, ws []float64 }{
		{[]float64{1.5, 2.25, 4, 7}, []float64{1, 3, 2, 1}},
		{[]float64{1.5, 2.25, 4, 7}, []float64{2, 1, 1, 2}},
		{[]float64{-3, 0.1, 0.2}, []float64{4, 1, 4}},
		{[]float64{5}, []float64{3}},
	} {
		var e []float64
		for i, v := range tc.vs {
			for range int(tc.ws[i]) {
				e = append(e, v)
			}
		}
		sort.Float64s(e)
		if g, w := weightedMedianSorted(tc.vs, tc.ws), median(e); g != w {
			t.Fatalf("%v × %v: weighted median %v, expanded %v", tc.vs, tc.ws, g, w)
		}
		ones := make([]float64, len(tc.vs))
		for i := range ones {
			ones[i] = 1
		}
		if g, w := weightedMedianSorted(tc.vs, ones), median(tc.vs); math.Float64bits(g) != math.Float64bits(w) {
			t.Fatalf("%v: unit-weight median %v, median %v", tc.vs, g, w)
		}
	}
}
