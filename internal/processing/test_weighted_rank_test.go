package processing

import (
	"encoding/json"
	"math"
	"slices"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// Frequency-weighted rank tests (E2-S1): TEST_MANN_WHITNEY_U,
// TEST_WILCOXON_SR, TEST_KRUSKAL_WALLIS, TEST_SPEARMAN_R and
// TEST_KENDALL_TAU are ClassFrequencyOnly. Oracles:
//   - unity: w ≡ 1 marshals byte-identically to the unweighted run once
//     sum_weights is shed;
//   - expansion: integer weights equal the physically expanded rows run
//     unweighted, every figure to 1e-10 relative (raw row counts aside);
//   - the shared weightedMidRanks equals midRanks on the expansion.

// rankFixture is weightedTestFixture rounded to whole numbers, so both
// columns carry ties (including heavy single rows) and some x − y
// differences are zero.
func rankFixture() []wRow {
	rows := weightedTestFixture()
	for i := range rows {
		rows[i].x = math.Round(rows[i].x)
		rows[i].y = math.Round(rows[i].y)
	}
	return rows
}

func rankTestCases() []weightedTestCase {
	all := rankFixture()
	two := onlyGroups(all, "a", "b")
	return []weightedTestCase{
		{"mann_whitney", types.Test{Type: types.TEST_MANN_WHITNEY_U, Field: "x", SplitBy: "g"}, two},
		{"wilcoxon_sr", types.Test{Type: types.TEST_WILCOXON_SR, Field: "x", Field2: "y"}, all},
		{"kruskal", types.Test{Type: types.TEST_KRUSKAL_WALLIS, Field: "x", SplitBy: "g"}, all},
		{"spearman", types.Test{Type: types.TEST_SPEARMAN_R, Field: "x", Field2: "y"}, all},
		{"kendall", types.Test{Type: types.TEST_KENDALL_TAU, Field: "x", Field2: "y"}, all},
	}
}

// rankRawCounts are details that stay RAW row counts under a weight
// (like `n`), so they are not compared against the expansion.
var rankRawCounts = map[string]bool{"n": true, "n_total": true, "zero_diffs": true, "sum_weights": true}

func expandRows(rows []wRow) []wRow {
	var out []wRow
	for _, r := range rows {
		for range int(r.w) {
			e := r
			e.w = 1
			out = append(out, e)
		}
	}
	return out
}

// TestWeightedMidRanks_EqualsExpansion: a row of weight w ranks like w
// identical rows; ties are the expansion's tie sizes; unit weights are
// midRanks bit for bit.
func TestWeightedMidRanks_EqualsExpansion(t *testing.T) {
	values := []float64{3, 1, 4, 1, 5, 9, 2, 6, 5, 3}
	weights := []float64{2, 1, 3, 1, 1, 4, 2, 1, 2, 1}
	var ev []float64
	var owner []int
	for i, v := range values {
		for range int(weights[i]) {
			ev = append(ev, v)
			owner = append(owner, i)
		}
	}
	er, eties := midRanks(ev)
	ranks, ties := weightedMidRanks(values, weights)
	for k, i := range owner {
		if ranks[i] != er[k] {
			t.Fatalf("rank of row %d = %v, expansion %v", i, ranks[i], er[k])
		}
	}
	slices.Sort(eties)
	got := slices.Clone(ties)
	slices.Sort(got)
	if len(got) != len(eties) {
		t.Fatalf("ties %v, expansion %v", got, eties)
	}
	for i := range got {
		if got[i] != float64(eties[i]) {
			t.Fatalf("ties %v, expansion %v", got, eties)
		}
	}
	if tieCorrectionW(ties) != tieCorrection(eties) || tiesDominateW(ties, float64(len(ev))) != tiesDominate(eties, len(ev)) {
		t.Fatal("tie correction / dominance differ from the expansion")
	}
	ur, uties := weightedMidRanks(values, nil)
	mr, mties := midRanks(values)
	if !slices.Equal(ur, mr) || len(uties) != len(mties) {
		t.Fatalf("unit weights %v %v, midRanks %v %v", ur, uties, mr, mties)
	}
}

// TestWeightedRankTests_UnityMatchesUnweighted: every weight 1 under
// kind frequency reproduces the unweighted result byte for byte and
// reports sum_weights = n (no n_eff).
func TestWeightedRankTests_UnityMatchesUnweighted(t *testing.T) {
	for _, tc := range rankTestCases() {
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
}

// numbersAny is numbers plus the int shapes the rank tests report.
func numbersAny(v any) []float64 {
	switch x := v.(type) {
	case int:
		return []float64{float64(x)}
	case []int:
		return intsToFloats(x)
	}
	return numbers(v)
}

// TestWeightedRankTests_FrequencyIsExpansion: integer frequency weights
// equal the expanded rows run unweighted — statistic, df, p-value, every
// numeric detail (effect sizes included) and the warnings; the raw row
// counts stay raw and sum_weights is the expanded n.
func TestWeightedRankTests_FrequencyIsExpansion(t *testing.T) {
	for _, tc := range rankTestCases() {
		got := runWeightedRowTest(t, tc.spec, tc.rows, types.WeightKindFrequency)
		want := runWeightedRowTest(t, tc.spec, expandRows(tc.rows), "")
		if !relEq(got.Statistic, want.Statistic, 1e-12) || !relEq(got.DF, want.DF, 1e-12) || !relEq(got.PValue, want.PValue, 1e-10) {
			t.Fatalf("%s: stat/df/p %v %v %v, want %v %v %v", tc.name, got.Statistic, got.DF, got.PValue, want.Statistic, want.DF, want.PValue)
		}
		if !slices.Equal(got.Warnings, want.Warnings) {
			t.Fatalf("%s: warnings %v, want %v", tc.name, got.Warnings, want.Warnings)
		}
		if gs, wn := numbersAny(got.Details["sum_weights"]), numbersAny(want.Details["n"]); !slices.Equal(gs, wn) {
			t.Fatalf("%s: sum_weights %v, want expanded n %v", tc.name, gs, wn)
		}
		// Wilcoxon's n counts the non-zero pairs; zero_diffs the rest.
		if gn := numbersAny(got.Details["n"]); sum(gn)+sum(numbersAny(got.Details["zero_diffs"])) != float64(len(tc.rows)) {
			t.Fatalf("%s: raw n %v, want %d rows", tc.name, gn, len(tc.rows))
		}
		for k, wv := range want.Details {
			if rankRawCounts[k] {
				continue
			}
			if es, ok := wv.(map[string]any); ok {
				for ek, ev := range es {
					g := got.Details[k].(map[string]any)[ek].(float64)
					if !relEq(g, ev.(float64), 1e-10) {
						t.Fatalf("%s: %s.%s = %v, want %v", tc.name, k, ek, g, ev)
					}
				}
				continue
			}
			wn, gn := numbersAny(wv), numbersAny(got.Details[k])
			if wn == nil {
				if _, isStr := wv.([]string); !isStr && wv != nil {
					t.Fatalf("%s: detail %s (%T) not compared", tc.name, k, wv)
				}
				continue // group labels / no ties
			}
			if len(wn) != len(gn) {
				t.Fatalf("%s: %s shape %v vs %v", tc.name, k, gn, wn)
			}
			for i := range wn {
				if !relEq(gn[i], wn[i], 1e-10) {
					t.Fatalf("%s: %s[%d] = %v, want %v", tc.name, k, i, gn[i], wn[i])
				}
			}
		}
	}
}

// TestWeightedRankTests_ExcludeInvalidAndZero: a row whose weight is
// null, negative, NaN, infinite, non-integer (kind frequency) or zero is
// dropped; the result equals the run without those rows.
func TestWeightedRankTests_ExcludeInvalidAndZero(t *testing.T) {
	schema := weightedTestSchema()
	for _, tc := range rankTestCases() {
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
			{"x": 99, "y": 1, "g": g},
			{"x": 99, "y": 1, "w": -2, "g": g},
			{"x": 99, "y": 1, "w": math.NaN(), "g": g},
			{"x": 99, "y": 1, "w": math.Inf(1), "g": g},
			{"x": 99, "y": 1, "w": 0, "g": g},
			{"x": 99, "y": 1, "w": 2.5, "g": g}, // non-integer under kind frequency: invalid
			{"x": 99, "y": 99, "w": 0, "g": g},  // zero weight on a zero diff: not counted in zero_diffs
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
}
