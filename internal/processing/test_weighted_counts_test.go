package processing

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Weighted count tests (E1-S3): TEST_PROP_Z and TEST_CHISQ under both
// weight kinds. Oracles:
//   - unity: w ≡ 1 under either kind marshals byte-identically to the
//     unweighted run once the weight-only keys are shed;
//   - frequency: integer weights equal the expanded rows run unweighted;
//   - probability: R 4.6.1 closed form — chisq.test(n_eff*prop.table(wtab),
//     correct=FALSE) and prop.test(x = p̂_g·n_eff_g, n = n_eff_g,
//     correct=FALSE) — plus the same in Go.

type cRow struct {
	g, o string
	w    float64
}

// countFixture: split g ∈ {a, b}, outcome o ∈ {yes, no, maybe}, uneven
// integer weights. The R oracle values below were generated from it.
func countFixture() []cRow {
	g := strings.Split("a a a a a a a a a a b b b b b b b b b b b", " ")
	o := strings.Split("yes no yes maybe no yes no maybe yes no no yes no no maybe yes no maybe no no yes", " ")
	w := []float64{3, 1, 2, 5, 1, 4, 2, 1, 3, 2, 1, 2, 6, 1, 3, 1, 2, 2, 4, 1, 1}
	out := make([]cRow, len(g))
	for i := range g {
		out[i] = cRow{g: g[i], o: o[i], w: w[i]}
	}
	return out
}

func countSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "g", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
		{Name: "o", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
		{Name: "w", Type: encoding.FieldTypeF64},
	}}
}

func countRecord(schema *encoding.Schema, r cRow) *Record {
	return NewRecord(schema, map[string]float64{
		"g": float64(dictIDOrAdd(schema, "g", r.g)),
		"o": float64(dictIDOrAdd(schema, "o", r.o)),
		"w": r.w,
	})
}

func runCountTest(t *testing.T, spec types.Test, rows []cRow, kind types.WeightKind) *types.TestResult {
	t.Helper()
	schema := countSchema()
	if kind != "" {
		spec.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: kind})
	}
	rt, err := rowTestRegistry[spec.Type](&spec, schema)
	if err != nil {
		t.Fatalf("%s factory: %v", spec.Type, err)
	}
	for _, r := range rows {
		if err := rt.UpdateRow(countRecord(schema, r)); err != nil {
			t.Fatal(err)
		}
	}
	res, err := rt.Finalize()
	if err != nil {
		t.Fatalf("%s finalize: %v", spec.Type, err)
	}
	return res
}

var (
	propZSpec = types.Test{Type: types.TEST_PROP_Z, Field: "o", SplitBy: "g", Params: json.RawMessage(`{"success":"yes"}`)}
	chiSqSpec = types.Test{Type: types.TEST_CHISQ, Rows: "g", Cols: "o"}
)

func countCases() []weightedCountCase {
	return []weightedCountCase{{"prop_z", propZSpec}, {"chisq", chiSqSpec}}
}

type weightedCountCase struct {
	name string
	spec types.Test
}

// TestWeightedCountTests_UnityMatchesUnweighted: every weight 1, under
// either kind, reproduces the unweighted result byte for byte.
func TestWeightedCountTests_UnityMatchesUnweighted(t *testing.T) {
	ones := countFixture()
	for i := range ones {
		ones[i].w = 1
	}
	for _, tc := range countCases() {
		base, _ := json.Marshal(runCountTest(t, tc.spec, ones, ""))
		for _, kind := range []types.WeightKind{types.WeightKindFrequency, types.WeightKindProbability} {
			got := runCountTest(t, tc.spec, ones, kind)
			if got.Details["sum_weights"] == nil || (got.Details["n_eff"] != nil) != (kind == types.WeightKindProbability) {
				t.Fatalf("%s/%s: weight keys %v / %v", tc.name, kind, got.Details["sum_weights"], got.Details["n_eff"])
			}
			if b := shedWeightKeys(got); string(b) != string(base) {
				t.Fatalf("%s/%s unity differs:\n got %s\nwant %s", tc.name, kind, b, base)
			}
		}
	}
}

// TestWeightedCountTests_FrequencyIsExpansion: integer frequency
// weights equal the expanded rows run unweighted — statistic, df,
// p-value and every numeric detail; `n` stays the raw row count,
// successes / the contingency become Σw.
func TestWeightedCountTests_FrequencyIsExpansion(t *testing.T) {
	rows := countFixture()
	var expanded []cRow
	for _, r := range rows {
		for range int(r.w) {
			expanded = append(expanded, r)
		}
	}
	for _, tc := range countCases() {
		got := runCountTest(t, tc.spec, rows, types.WeightKindFrequency)
		want := runCountTest(t, tc.spec, expanded, "")
		if !relEq(got.Statistic, want.Statistic, 1e-12) || got.DF != want.DF || !relEq(got.PValue, want.PValue, 1e-10) {
			t.Fatalf("%s: stat/df/p %v %v %v, want %v %v %v", tc.name, got.Statistic, got.DF, got.PValue, want.Statistic, want.DF, want.PValue)
		}
		if got.Details["n_eff"] != nil {
			t.Fatalf("%s: n_eff under frequency", tc.name)
		}
		if gs, wn := numbers(got.Details["sum_weights"]), numbers(want.Details["n"]); len(gs) != len(wn) || sum(gs) != sum(wn) {
			t.Fatalf("%s: sum_weights %v, want expanded n %v", tc.name, gs, wn)
		}
		if gn := numbers(got.Details["n"]); sum(gn) != float64(len(rows)) {
			t.Fatalf("%s: raw n %v, want %d rows", tc.name, gn, len(rows))
		}
		for k, wv := range want.Details {
			if k == "n" {
				continue
			}
			if es, ok := wv.(map[string]any); ok {
				for ek, ev := range es {
					if g := got.Details[k].(map[string]any)[ek].(float64); !relEq(g, ev.(float64), 1e-10) {
						t.Fatalf("%s: %s.%s = %v, want %v", tc.name, k, ek, g, ev)
					}
				}
				continue
			}
			if k == "contingency" {
				g, w := got.Details[k].([][]float64), wv.([][]int64)
				for i := range w {
					for j := range w[i] {
						if g[i][j] != float64(w[i][j]) {
							t.Fatalf("%s: contingency %v, want %v", tc.name, g, w)
						}
					}
				}
				continue
			}
			wn, gn := numbers(wv), numbers(got.Details[k])
			if wn == nil {
				continue // labels
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

// TestWeightedCountTests_ROracle pins both kinds to R 4.6.1:
//
//	wtab <- xtabs(w ~ g + o); neff <- sum(w)^2/sum(w^2)
//	chisq.test(neff*prop.table(wtab), correct=FALSE)           # probability
//	chisq.test(table(rep(g,w), rep(o,w)), correct=FALSE)       # frequency
//	prop.test(x = p̂_g·N*_g, n = N*_g, correct=FALSE)           # z = ±√X²
//
// (scratch oracle script; the values are the 17-digit R output).
func TestWeightedCountTests_ROracle(t *testing.T) {
	rows := countFixture()
	for _, c := range []struct {
		spec        types.Test
		kind        types.WeightKind
		stat, p, df float64
	}{
		{chiSqSpec, types.WeightKindProbability, 2.5099111414900883, 0.28508851421336423, 2},
		{chiSqSpec, types.WeightKindFrequency, 7.9480519480519485, 0.018797601933218677, 2},
		{propZSpec, types.WeightKindProbability, 1.3720476799868218, 0.17004859529831626, 0},
		{propZSpec, types.WeightKindFrequency, 2.4494897427831779, 0.014305878435429645, 0},
	} {
		got := runCountTest(t, c.spec, rows, c.kind)
		if !relEq(got.Statistic, c.stat, 1e-12) || !relEq(got.PValue, c.p, 1e-8) || got.DF != c.df {
			t.Fatalf("%s/%s: stat %v p %v df %v, want %v %v %v", c.spec.Type, c.kind, got.Statistic, got.PValue, got.DF, c.stat, c.p, c.df)
		}
	}
	// χ² probability: the scaled table's expected minimum and n_eff.
	prob := runCountTest(t, chiSqSpec, rows, types.WeightKindProbability)
	if !relEq(prob.Details["expected_min"].(float64), 1.736842105263158, 1e-12) || !relEq(prob.Details["n_eff"].(float64), 15.157894736842104, 1e-12) {
		t.Fatalf("expected_min %v n_eff %v", prob.Details["expected_min"], prob.Details["n_eff"])
	}
	if prob.Details["n"] != int64(len(rows)) || prob.Details["sum_weights"] != 48.0 {
		t.Fatalf("n %v sum_weights %v", prob.Details["n"], prob.Details["sum_weights"])
	}
}

// TestWeightedCountTests_ClosedForm recomputes both statistics in Go
// from the weighted table: χ² = n_eff·Σ(p_ij − p_i·p_j)²/(p_i·p_j)
// with Cramér's V on n_eff; prop-z with p̂_g = Σw_yes/Σw_g, N*_g.
func TestWeightedCountTests_ClosedForm(t *testing.T) {
	rows := countFixture()
	for _, prob := range []bool{false, true} {
		kind := map[bool]types.WeightKind{false: types.WeightKindFrequency, true: types.WeightKindProbability}[prob]
		// χ².
		cell := map[[2]string]float64{}
		rowM, colM := map[string]float64{}, map[string]float64{}
		var sw, sw2 float64
		for _, r := range rows {
			cell[[2]string{r.g, r.o}] += r.w
			rowM[r.g] += r.w
			colM[r.o] += r.w
			sw += r.w
			sw2 += r.w * r.w
		}
		nStar := sw
		if prob {
			nStar = sw * sw / sw2
		}
		var x2 float64
		for g, rm := range rowM {
			for o, cm := range colM {
				pij, pi, pj := cell[[2]string{g, o}]/sw, rm/sw, cm/sw
				x2 += nStar * (pij - pi*pj) * (pij - pi*pj) / (pi * pj)
			}
		}
		got := runCountTest(t, chiSqSpec, rows, kind)
		if !relEq(got.Statistic, x2, 1e-10) {
			t.Fatalf("chisq/%s: %v, want %v", kind, got.Statistic, x2)
		}
		wantV := math.Sqrt(x2 / (nStar * 1))
		if v := got.Details["effect_size"].(map[string]any)["cramers_v"].(float64); !relEq(v, wantV, 1e-10) {
			t.Fatalf("chisq/%s: cramers_v %v, want %v", kind, v, wantV)
		}
		// prop-z.
		var pHat, ns, mass [2]float64
		for i, g := range []string{"a", "b"} {
			var s, s2, yes float64
			for _, r := range rows {
				if r.g == g {
					s += r.w
					s2 += r.w * r.w
					if r.o == "yes" {
						yes += r.w
					}
				}
			}
			pHat[i] = yes / s
			ns[i] = s
			if prob {
				ns[i] = s * s / s2
			}
			mass[i] = pHat[i] * ns[i]
		}
		pooled := (mass[0] + mass[1]) / (ns[0] + ns[1])
		z := (pHat[0] - pHat[1]) / math.Sqrt(pooled*(1-pooled)*(1/ns[0]+1/ns[1]))
		pz := runCountTest(t, propZSpec, rows, kind)
		if !relEq(pz.Statistic, z, 1e-10) || !relEq(pz.Details["pooled"].(float64), pooled, 1e-12) {
			t.Fatalf("prop_z/%s: z %v pooled %v, want %v %v", kind, pz.Statistic, pz.Details["pooled"], z, pooled)
		}
		if p := pz.Details["proportion"].([]float64); !relEq(p[0], pHat[0], 1e-15) || !relEq(p[1], pHat[1], 1e-15) {
			t.Fatalf("prop_z/%s: proportion %v, want %v", kind, p, pHat)
		}
		if s := pz.Details["successes"].([]float64); len(s) != 2 {
			t.Fatalf("prop_z/%s: successes %v not Σw", kind, s)
		}
		if prob && len(numbers(pz.Details["n_eff"])) != 2 {
			t.Fatalf("prop_z: n_eff %v not shaped like n", pz.Details["n_eff"])
		}
	}
}

// TestWeightedCountTests_ExpectedGuardOnScaledTable: a balanced 2×2
// with every row weighing 4 has Σw expected counts of 8 (no warning
// under frequency) but n_eff-scaled expected counts of 2 — the guard
// reads the scaled table and warns under probability.
func TestWeightedCountTests_ExpectedGuardOnScaledTable(t *testing.T) {
	var rows []cRow
	for _, g := range []string{"a", "b"} {
		for _, o := range []string{"yes", "no"} {
			n := 2
			if g == "a" && o == "yes" {
				n = 3
			}
			for range n {
				rows = append(rows, cRow{g: g, o: o, w: 4})
			}
		}
	}
	low := func(res *types.TestResult) bool {
		for _, w := range res.Warnings {
			if strings.Contains(w, string(errors.PULSE_TEST_EXPECTED_COUNT_TOO_LOW)) {
				return true
			}
		}
		return false
	}
	freq := runCountTest(t, chiSqSpec, rows, types.WeightKindFrequency)
	prob := runCountTest(t, chiSqSpec, rows, types.WeightKindProbability)
	if low(freq) || freq.Details["expected_min"].(float64) < 5 {
		t.Fatalf("frequency: expected_min %v warnings %v", freq.Details["expected_min"], freq.Warnings)
	}
	if !low(prob) || prob.Details["expected_min"].(float64) >= 5 {
		t.Fatalf("probability: expected_min %v warnings %v", prob.Details["expected_min"], prob.Warnings)
	}
}

// TestWeightedCountTests_ExcludeInvalidAndZero: null, negative, NaN,
// +Inf and zero weights drop the row; the result equals the run
// without them.
func TestWeightedCountTests_ExcludeInvalidAndZero(t *testing.T) {
	for _, tc := range countCases() {
		schema := countSchema()
		spec := tc.spec
		spec.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindProbability})
		rt, err := rowTestRegistry[spec.Type](&spec, schema)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range countFixture() {
			_ = rt.UpdateRow(countRecord(schema, r))
		}
		a, yes := float64(dictIDOrAdd(schema, "g", "a")), float64(dictIDOrAdd(schema, "o", "yes"))
		for _, bad := range []map[string]float64{
			{"g": a, "o": yes},
			{"g": a, "o": yes, "w": -2},
			{"g": a, "o": yes, "w": math.NaN()},
			{"g": a, "o": yes, "w": math.Inf(1)},
			{"g": a, "o": yes, "w": 0},
		} {
			_ = rt.UpdateRow(NewRecord(schema, bad))
		}
		got, err := rt.Finalize()
		if err != nil {
			t.Fatal(err)
		}
		want := runCountTest(t, tc.spec, countFixture(), types.WeightKindProbability)
		ga, _ := json.Marshal(got)
		wa, _ := json.Marshal(want)
		if string(ga) != string(wa) {
			t.Fatalf("%s: invalid rows leaked:\n got %s\nwant %s", tc.name, ga, wa)
		}
	}
}

// TestWeightedCountTests_ProcessorStamps: a request weight reaches both
// tests through the processor (streaming path) and streaming equals
// buffered.
func TestWeightedCountTests_ProcessorStamps(t *testing.T) {
	schema := countSchema()
	var recs []*Record
	for _, r := range countFixture() {
		recs = append(recs, countRecord(schema, r))
	}
	req := func(grouped bool) *types.Request {
		pz, cs := propZSpec, chiSqSpec
		r := &types.Request{Weight: &types.WeightSpec{Field: "w", Kind: types.WeightKindProbability},
			Tests:        []*types.Test{&pz, &cs},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "w", Weight: types.NullSlotWeight()}}}
		if grouped {
			r.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}}
		}
		return r
	}
	var out [2][]byte
	for i, grouped := range []bool{false, true} {
		resp, err := NewProcessor(schema).Process(context.Background(), req(grouped), NewSliceIterator(recs))
		if err != nil {
			t.Fatal(err)
		}
		for _, tr := range resp.Tests {
			if tr.Details["n_eff"] == nil {
				t.Fatalf("grouped=%v %s not weighted: %v", grouped, tr.Type, tr.Details)
			}
		}
		out[i], _ = json.Marshal(resp.Tests)
	}
	if string(out[0]) != string(out[1]) {
		t.Fatalf("streaming and buffered differ:\n%s\n%s", out[0], out[1])
	}
}
