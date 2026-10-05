package processing

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/statdist"
	"github.com/frankbardon/pulse/types"
)

// Weighted moment tests (E1-S2): TEST_T (one-sample + split), TEST_WELCH,
// TEST_PAIRED_T, TEST_Z_TWO_SAMPLE, TEST_ANOVA_F, TEST_ANOVA_WELCH and
// TEST_PEARSON_R under both weight kinds. Three oracles:
//   - unity: w ≡ 1 under either kind marshals byte-identically to the
//     unweighted run once the weight-only keys are shed;
//   - frequency: integer weights equal the physically expanded rows run
//     unweighted, and match the closed form below;
//   - probability: the closed form on w* (two-pass sums, no Welford),
//     N* = Kish n_eff.

type wRow struct {
	x, y float64
	g    string
	w    float64
}

// weightedTestFixture: three groups, uneven integer weights.
func weightedTestFixture() []wRow {
	xs := map[string][]float64{
		"a": {4.1, 5.3, 6.0, 7.2, 5.5},
		"b": {6.8, 7.9, 6.1, 8.4, 9.0, 7.7},
		"c": {3.3, 4.8, 5.9, 4.4},
	}
	ws := map[string][]float64{
		"a": {1, 3, 2, 1, 4},
		"b": {2, 1, 1, 3, 2, 5},
		"c": {1, 2, 4, 1},
	}
	var out []wRow
	i := 0
	for _, g := range []string{"a", "b", "c"} {
		for j, x := range xs[g] {
			y := 0.6*x + []float64{0.9, -0.4, 1.3, 0.2, -0.8, 0.5, -1.1}[i%7]
			out = append(out, wRow{x: x, y: y, g: g, w: ws[g][j]})
			i++
		}
	}
	return out
}

func onlyGroups(rows []wRow, gs ...string) []wRow {
	var out []wRow
	for _, r := range rows {
		for _, g := range gs {
			if r.g == g {
				out = append(out, r)
			}
		}
	}
	return out
}

func weightedTestSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "x", Type: encoding.FieldTypeF64},
		{Name: "y", Type: encoding.FieldTypeF64},
		{Name: "w", Type: encoding.FieldTypeF64},
		{Name: "g", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
	}}
}

// runWeightedRowTest folds rows through spec's row test. kind "" runs
// unweighted (the weight column is still present on every row).
func runWeightedRowTest(t *testing.T, spec types.Test, rows []wRow, kind types.WeightKind) *types.TestResult {
	t.Helper()
	schema := weightedTestSchema()
	if kind != "" {
		spec.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: kind})
	}
	rt, err := rowTestRegistry[spec.Type](&spec, schema)
	if err != nil {
		t.Fatalf("%s factory: %v", spec.Type, err)
	}
	for _, r := range rows {
		rec := NewRecord(schema, map[string]float64{"x": r.x, "y": r.y, "w": r.w, "g": float64(dictIDOrAdd(schema, "g", r.g))})
		if err := rt.UpdateRow(rec); err != nil {
			t.Fatal(err)
		}
	}
	res, err := rt.Finalize()
	if err != nil {
		t.Fatalf("%s finalize: %v", spec.Type, err)
	}
	return res
}

// weightedTestCase is one test spec over the fixture rows it reads.
type weightedTestCase struct {
	name string
	spec types.Test
	rows []wRow
}

func weightedTestCases() []weightedTestCase {
	all := weightedTestFixture()
	two := onlyGroups(all, "a", "b")
	return []weightedTestCase{
		{"t_one_sample", types.Test{Type: types.TEST_T, Field: "x", Params: json.RawMessage(`{"mu":5.5}`)}, all},
		{"t_split", types.Test{Type: types.TEST_T, Field: "x", SplitBy: "g"}, two},
		{"welch", types.Test{Type: types.TEST_WELCH, Field: "x", SplitBy: "g"}, two},
		{"paired", types.Test{Type: types.TEST_PAIRED_T, Field: "x", Field2: "y"}, all},
		{"z", types.Test{Type: types.TEST_Z_TWO_SAMPLE, Field: "x", SplitBy: "g"}, two},
		{"anova_f", types.Test{Type: types.TEST_ANOVA_F, Field: "x", SplitBy: "g"}, all},
		{"anova_welch", types.Test{Type: types.TEST_ANOVA_WELCH, Field: "x", SplitBy: "g"}, all},
		{"pearson", types.Test{Type: types.TEST_PEARSON_R, Field: "x", Field2: "y"}, all},
	}
}

func shedWeightKeys(res *types.TestResult) []byte {
	c := *res
	c.Details = map[string]any{}
	for k, v := range res.Details {
		if k != "sum_weights" && k != "n_eff" {
			c.Details[k] = v
		}
	}
	b, _ := json.Marshal(&c)
	return b
}

// TestWeightedTests_UnityMatchesUnweighted: every weight 1, under either
// kind, reproduces the unweighted result byte for byte (statistic, df,
// p-value, every detail) — the op-order exactness rule.
func TestWeightedTests_UnityMatchesUnweighted(t *testing.T) {
	for _, tc := range weightedTestCases() {
		ones := append([]wRow(nil), tc.rows...)
		for i := range ones {
			ones[i].w = 1
		}
		base, _ := json.Marshal(runWeightedRowTest(t, tc.spec, tc.rows, ""))
		for _, kind := range []types.WeightKind{types.WeightKindFrequency, types.WeightKindProbability} {
			got := runWeightedRowTest(t, tc.spec, ones, kind)
			if got.Details["sum_weights"] == nil {
				t.Fatalf("%s/%s: no sum_weights", tc.name, kind)
			}
			if (got.Details["n_eff"] != nil) != (kind == types.WeightKindProbability) {
				t.Fatalf("%s/%s: n_eff presence wrong: %v", tc.name, kind, got.Details["n_eff"])
			}
			if b := shedWeightKeys(got); string(b) != string(base) {
				t.Fatalf("%s/%s unity differs:\n got %s\nwant %s", tc.name, kind, b, base)
			}
		}
	}
}

// relEq compares with a relative tolerance.
func relEq(a, b, tol float64) bool {
	if a == b {
		return true
	}
	return math.Abs(a-b) <= tol*math.Max(math.Abs(a), math.Abs(b))
}

func numbers(v any) []float64 {
	switch x := v.(type) {
	case float64:
		return []float64{x}
	case int64:
		return []float64{float64(x)}
	case []float64:
		return x
	case []int64:
		out := make([]float64, len(x))
		for i, n := range x {
			out[i] = float64(n)
		}
		return out
	}
	return nil
}

// TestWeightedTests_FrequencyIsExpansion: integer frequency weights
// equal the expanded rows run unweighted — statistic, df, p-value and
// every numeric detail to 1e-12 relative; `n` stays the raw row count
// and sum_weights is exactly the expanded n.
func TestWeightedTests_FrequencyIsExpansion(t *testing.T) {
	for _, tc := range weightedTestCases() {
		var expanded []wRow
		for _, r := range tc.rows {
			for range int(r.w) {
				expanded = append(expanded, r)
			}
		}
		got := runWeightedRowTest(t, tc.spec, tc.rows, types.WeightKindFrequency)
		want := runWeightedRowTest(t, tc.spec, expanded, "")
		if !relEq(got.Statistic, want.Statistic, 1e-12) || !relEq(got.DF, want.DF, 1e-12) || !relEq(got.PValue, want.PValue, 1e-10) {
			t.Fatalf("%s: stat/df/p %v %v %v, want %v %v %v", tc.name, got.Statistic, got.DF, got.PValue, want.Statistic, want.DF, want.PValue)
		}
		if got.Details["n_eff"] != nil {
			t.Fatalf("%s: n_eff under frequency", tc.name)
		}
		if gs, wn := numbers(got.Details["sum_weights"]), numbers(want.Details["n"]); len(gs) != len(wn) || !relEq(sum(gs), sum(wn), 0) {
			t.Fatalf("%s: sum_weights %v, want expanded n %v", tc.name, gs, wn)
		}
		if gn := numbers(got.Details["n"]); sum(gn) != float64(len(tc.rows)) {
			t.Fatalf("%s: raw n %v, want %d rows", tc.name, gn, len(tc.rows))
		}
		for k, wv := range want.Details {
			if k == "n" {
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
			wn, gn := numbers(wv), numbers(got.Details[k])
			if wn == nil {
				continue // group labels
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

func sum(xs []float64) float64 {
	var s float64
	for _, x := range xs {
		s += x
	}
	return s
}

// refSample is the closed-form w* summary of one sample: N*, weighted
// mean, w*-variance (two-pass sums, independent of the Welford code).
type refSample struct {
	sumW, sumWSq, nStar, mean, m2, c, s2 float64
}

func refOf(xs, ws []float64, prob bool) refSample {
	var r refSample
	var swx float64
	for i, x := range xs {
		r.sumW += ws[i]
		r.sumWSq += ws[i] * ws[i]
		swx += ws[i] * x
	}
	r.mean = swx / r.sumW
	for i, x := range xs {
		r.m2 += ws[i] * (x - r.mean) * (x - r.mean)
	}
	r.nStar = r.sumW
	if prob {
		r.nStar = r.sumW * r.sumW / r.sumWSq
	}
	r.c = r.nStar / r.sumW
	r.s2 = r.c * r.m2 / (r.nStar - 1)
	return r
}

func colOf(rows []wRow, f func(wRow) float64) []float64 {
	out := make([]float64, len(rows))
	for i, r := range rows {
		out[i] = f(r)
	}
	return out
}

func weightsOf(rows []wRow) []float64 { return colOf(rows, func(r wRow) float64 { return r.w }) }

// closedForm returns the expected (statistic, df, p) of a case under a
// kind by the closed-form w* formulas (.claude/reference/weighting.md,
// Weighted inference).
func closedForm(tc weightedTestCase, prob bool) (stat, df, p float64) {
	x := func(r wRow) float64 { return r.x }
	welch := func() (float64, float64, float64) {
		a, b := onlyGroups(tc.rows, "a"), onlyGroups(tc.rows, "b")
		ra, rb := refOf(colOf(a, x), weightsOf(a), prob), refOf(colOf(b, x), weightsOf(b), prob)
		va, vb := ra.s2/ra.nStar, rb.s2/rb.nStar
		stat := (ra.mean - rb.mean) / math.Sqrt(va+vb)
		df := (va + vb) * (va + vb) / (va*va/(ra.nStar-1) + vb*vb/(rb.nStar-1))
		return stat, df, statdist.StudentTTwoSidedP(stat, df)
	}
	switch tc.spec.Type {
	case types.TEST_T, types.TEST_WELCH:
		if tc.spec.SplitBy != "" {
			return welch()
		}
		r := refOf(colOf(tc.rows, x), weightsOf(tc.rows), prob)
		stat = (r.mean - 5.5) / math.Sqrt(r.s2/r.nStar)
		df = r.nStar - 1
		return stat, df, statdist.StudentTTwoSidedP(stat, df)
	case types.TEST_Z_TWO_SAMPLE:
		stat, _, _ = welch()
		return stat, 0, math.Erfc(math.Abs(stat) / math.Sqrt2)
	case types.TEST_PAIRED_T:
		r := refOf(colOf(tc.rows, func(r wRow) float64 { return r.x - r.y }), weightsOf(tc.rows), prob)
		stat = r.mean / math.Sqrt(r.s2/r.nStar)
		df = r.nStar - 1
		return stat, df, statdist.StudentTTwoSidedP(stat, df)
	case types.TEST_ANOVA_F:
		all := refOf(colOf(tc.rows, x), weightsOf(tc.rows), prob)
		var ssb, ssw float64
		for _, g := range []string{"a", "b", "c"} {
			rg := refOf(colOf(onlyGroups(tc.rows, g), x), weightsOf(onlyGroups(tc.rows, g)), false)
			ssb += all.c * rg.sumW * (rg.mean - all.mean) * (rg.mean - all.mean)
			ssw += all.c * rg.m2
		}
		dfw := all.nStar - 3
		stat = (ssb / 2) / (ssw / dfw)
		return stat, 2, fSurvival(stat, 2, dfw)
	case types.TEST_ANOVA_WELCH:
		var v, vm, W float64
		groups := []refSample{}
		for _, g := range []string{"a", "b", "c"} {
			rg := refOf(colOf(onlyGroups(tc.rows, g), x), weightsOf(onlyGroups(tc.rows, g)), prob)
			groups = append(groups, rg)
			v = rg.nStar / rg.s2
			W += v
			vm += v * rg.mean
		}
		mt := vm / W
		var num, tail float64
		for _, rg := range groups {
			v = rg.nStar / rg.s2
			num += v * (rg.mean - mt) * (rg.mean - mt)
			tail += (1 - v/W) * (1 - v/W) / (rg.nStar - 1)
		}
		stat = (num / 2) / (1 + 2*(1.0/8)*tail)
		df2 := 8 / (3 * tail)
		return stat, 2, fSurvival(stat, 2, df2)
	case types.TEST_PEARSON_R:
		w := weightsOf(tc.rows)
		rx := refOf(colOf(tc.rows, x), w, prob)
		ry := refOf(colOf(tc.rows, func(r wRow) float64 { return r.y }), w, prob)
		var cxy float64
		for i, r := range tc.rows {
			cxy += w[i] * (r.x - rx.mean) * (r.y - ry.mean)
		}
		stat = cxy / math.Sqrt(rx.m2*ry.m2)
		df = rx.nStar - 2
		return stat, df, statdist.StudentTTwoSidedP(stat*math.Sqrt(df/(1-stat*stat)), df)
	}
	panic("no closed form for " + string(tc.spec.Type))
}

// TestWeightedTests_ClosedForm: both kinds match the hand-written w*
// formulas — frequency with N* = Σw, probability with N* = Kish n_eff
// (fractional df) — and report n_eff in the shape of `n`.
func TestWeightedTests_ClosedForm(t *testing.T) {
	for _, tc := range weightedTestCases() {
		for _, kind := range []types.WeightKind{types.WeightKindFrequency, types.WeightKindProbability} {
			prob := kind == types.WeightKindProbability
			got := runWeightedRowTest(t, tc.spec, tc.rows, kind)
			stat, df, p := closedForm(tc, prob)
			if !relEq(got.Statistic, stat, 1e-10) || !relEq(got.DF, df, 1e-10) || !relEq(got.PValue, p, 1e-8) {
				t.Fatalf("%s/%s: stat/df/p %v %v %v, want %v %v %v", tc.name, kind, got.Statistic, got.DF, got.PValue, stat, df, p)
			}
			if prob {
				if len(numbers(got.Details["n_eff"])) != len(numbers(got.Details["n"])) {
					t.Fatalf("%s: n_eff %v not shaped like n %v", tc.name, got.Details["n_eff"], got.Details["n"])
				}
				if df == math.Trunc(df) && tc.spec.Type != types.TEST_ANOVA_F && tc.spec.Type != types.TEST_ANOVA_WELCH && tc.spec.Type != types.TEST_Z_TWO_SAMPLE {
					t.Fatalf("%s: probability df %v is integral — N* not n_eff?", tc.name, df)
				}
			}
		}
	}
}

// TestWeightedTests_HandComputedOneSample pins one tiny case by hand:
// x = {1, 3, 8}, w = {1, 2, 1}, μ0 = 0. Probability: Σw = 4, n_eff = 8/3,
// mean 3.75, s² = 10.7 (inference_test.go), se = √(10.7/(8/3)),
// df = 5/3. Frequency: s² = 26.75/3, se = √(s²/4), df = 3.
func TestWeightedTests_HandComputedOneSample(t *testing.T) {
	rows := []wRow{{x: 1, w: 1}, {x: 3, w: 2}, {x: 8, w: 1}}
	spec := types.Test{Type: types.TEST_T, Field: "x"}
	prob := runWeightedRowTest(t, spec, rows, types.WeightKindProbability)
	wantT := 3.75 / math.Sqrt(10.7/(8.0/3))
	if !relEq(prob.Statistic, wantT, 1e-12) || !relEq(prob.DF, 5.0/3, 1e-12) || !relEq(prob.Details["n_eff"].(float64), 8.0/3, 1e-12) {
		t.Fatalf("probability: t %v df %v n_eff %v, want %v 5/3 8/3", prob.Statistic, prob.DF, prob.Details["n_eff"], wantT)
	}
	if prob.Details["n"] != int64(3) || prob.Details["sum_weights"] != 4.0 {
		t.Fatalf("n / sum_weights = %v / %v", prob.Details["n"], prob.Details["sum_weights"])
	}
	freq := runWeightedRowTest(t, spec, rows, types.WeightKindFrequency)
	wantF := 3.75 / math.Sqrt(26.75/3/4)
	if !relEq(freq.Statistic, wantF, 1e-12) || freq.DF != 3 {
		t.Fatalf("frequency: t %v df %v, want %v 3", freq.Statistic, freq.DF, wantF)
	}
	if !relEq(prob.Details["variance"].(float64), 10.7, 1e-12) {
		t.Fatalf("probability variance %v", prob.Details["variance"])
	}
}

// TestWeightedTests_ExcludeInvalidAndZero: a row whose weight is null,
// negative, NaN or zero is dropped (and, null-valued rows aside, never
// enters `n`); the result equals the run without those rows. A paired
// row with a null second field is dropped before its (invalid) weight
// is judged.
func TestWeightedTests_ExcludeInvalidAndZero(t *testing.T) {
	schema := weightedTestSchema()
	for _, tc := range weightedTestCases() {
		spec := tc.spec
		spec.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindProbability})
		rt, err := rowTestRegistry[spec.Type](&spec, schema)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range tc.rows {
			_ = rt.UpdateRow(NewRecord(schema, map[string]float64{"x": r.x, "y": r.y, "w": r.w, "g": float64(dictIDOrAdd(schema, "g", r.g))}))
		}
		g := float64(dictIDOrAdd(schema, "g", tc.rows[0].g))
		for _, bad := range []map[string]float64{
			{"x": 99, "y": 1, "g": g},                   // null weight
			{"x": 99, "y": 1, "w": -2, "g": g},          // negative
			{"x": 99, "y": 1, "w": math.NaN(), "g": g},  // NaN
			{"x": 99, "y": 1, "w": math.Inf(1), "g": g}, // +Inf
			{"x": 99, "y": 1, "w": 0, "g": g},           // zero contributes nothing
			{"x": 99, "w": -1, "g": g},                  // null y (paired / pearson drop the pair first)
		} {
			_ = rt.UpdateRow(NewRecord(schema, bad))
		}
		got, err := rt.Finalize()
		if err != nil {
			t.Fatal(err)
		}
		want := runWeightedRowTest(t, tc.spec, tc.rows, types.WeightKindProbability)
		if a, b := shedWeightKeys(got), shedWeightKeys(want); string(a) != string(b) {
			t.Fatalf("%s: invalid rows leaked:\n got %s\nwant %s", tc.name, a, b)
		}
	}
}

// TestWeightedTests_ProcessorStampsAndTallies: through the processor, a
// request weight reaches a built-in row test on both the streaming
// (tests only) and the buffered (grouped) path with identical results;
// an invalid-weight row is excluded and tallied in
// PULSE_WEIGHT_INVALID_ROWS; a post-test of the same family is never
// stamped.
func TestWeightedTests_ProcessorStampsAndTallies(t *testing.T) {
	schema := weightedTestSchema()
	var recs []*Record
	for _, r := range weightedTestFixture() {
		recs = append(recs, NewRecord(schema, map[string]float64{"x": r.x, "y": r.y, "w": r.w, "g": float64(dictIDOrAdd(schema, "g", r.g))}))
	}
	recs = append(recs, NewRecord(schema, map[string]float64{"x": 50, "y": 1, "w": -1, "g": 0}))
	w := &types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency}
	req := func(grouped bool) *types.Request {
		r := &types.Request{Weight: w, Tests: []*types.Test{{Type: types.TEST_ANOVA_F, Field: "x", SplitBy: "g"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Weight: types.NullSlotWeight()}}}
		if grouped {
			r.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}}
		}
		return r
	}
	var results [2][]byte
	for i, grouped := range []bool{false, true} {
		p := NewProcessor(schema)
		resp, err := p.Process(context.Background(), req(grouped), NewSliceIterator(recs))
		if err != nil {
			t.Fatal(err)
		}
		wantPath := map[bool]ProcessPath{false: PathStreaming, true: PathBuffered}[grouped]
		if p.LastPath() != wantPath {
			t.Fatalf("grouped=%v ran %v", grouped, p.LastPath())
		}
		if resp.Tests[0].Details["sum_weights"] == nil {
			t.Fatalf("grouped=%v: test not weighted: %v", grouped, resp.Tests[0].Details)
		}
		var tallied bool
		for _, wn := range resp.Warnings {
			if wn.Code == string(errors.PULSE_WEIGHT_INVALID_ROWS) && wn.Details["count"] == int64(1) {
				tallied = true
			}
		}
		if !tallied {
			t.Fatalf("grouped=%v: invalid row not tallied: %+v", grouped, resp.Warnings)
		}
		results[i], _ = json.Marshal(resp.Tests)
	}
	if string(results[0]) != string(results[1]) {
		t.Fatalf("streaming and buffered differ:\n%s\n%s", results[0], results[1])
	}
	stamped := StampWeights(&types.Request{Weight: w,
		Tests:     []*types.Test{{Type: types.TEST_PEARSON_R}, {Type: types.TEST_FISHER_EXACT}},
		PostTests: []*types.Test{{Type: types.TEST_PEARSON_R}}}, nil)
	if stamped.Tests[0].Weight.Spec() == nil || stamped.Tests[1].Weight.Spec() != nil || stamped.PostTests[0].Weight.Spec() != nil {
		t.Fatalf("stamping: tests %v %v post %v", stamped.Tests[0].Weight, stamped.Tests[1].Weight, stamped.PostTests[0].Weight)
	}
}

// TestWeightedTests_LowNEff: very uneven probability weights leave a
// group's n_eff below the floor — one PULSE_WEIGHT_LOW_NEFF warning
// naming the group, an error under strict; frequency weights never warn.
func TestWeightedTests_LowNEff(t *testing.T) {
	schema := weightedTestSchema()
	var recs []*Record
	add := func(x, w float64, g string) {
		recs = append(recs, NewRecord(schema, map[string]float64{"x": x, "y": x, "w": w, "g": float64(dictIDOrAdd(schema, "g", g))}))
	}
	for i, x := range []float64{1, 2, 3, 4, 5} {
		add(x, 1, "a")
		add(x*1.5+0.2*float64(i), []float64{1000, 1, 1, 1, 1}[i], "b")
	}
	// mode: the three finalize sites — streaming, buffered (grouped) and
	// the buffered crosstab arm.
	mode := "streaming"
	run := func(kind types.WeightKind, strict bool) (*types.Response, error) {
		p := NewProcessor(schema)
		p.SetWeighting(nil, strict)
		req := &types.Request{
			Weight:       &types.WeightSpec{Field: "w", Kind: kind},
			Tests:        []*types.Test{{Type: types.TEST_WELCH, Field: "x", SplitBy: "g", Label: "wt"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x", Weight: types.NullSlotWeight()}},
		}
		switch mode {
		case "buffered":
			req.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}}
		case "crosstab":
			req.Aggregations = nil
			req.Crosstab = &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
				Columns: []*types.Group{{Type: types.GROUP_RANGE, Field: "y", Interval: 5}},
				Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "x", Weight: types.NullSlotWeight()},
			}
			resp, err := p.RunCrosstab(context.Background(), req, recs)
			return resp, err
		}
		return p.Process(context.Background(), req, NewSliceIterator(recs))
	}
	for _, m := range []string{"streaming", "buffered", "crosstab"} {
		mode = m
		t.Run(m, func(t *testing.T) { checkLowNEff(t, run) })
	}
}

func checkLowNEff(t *testing.T, run func(types.WeightKind, bool) (*types.Response, error)) {
	t.Helper()
	resp, err := run(types.WeightKindProbability, false)
	if err != nil {
		t.Fatal(err)
	}
	var low []*types.ResponseWarning
	for _, w := range resp.Warnings {
		if w.Code == string(errors.PULSE_WEIGHT_LOW_NEFF) {
			low = append(low, w)
		}
	}
	if len(low) != 1 || low[0].Details["group"] != "b" || low[0].Details["test"] != "wt" ||
		low[0].Details["min_required"] != 2 || low[0].Details["n_eff"].(float64) >= 2 || !strings.Contains(low[0].Message, `"b"`) {
		t.Fatalf("low-n_eff warnings = %+v", low)
	}
	if _, err := run(types.WeightKindProbability, true); err == nil || !strings.Contains(err.Error(), "n_eff") {
		t.Fatalf("strict: %v", err)
	} else if ce, ok := err.(*errors.CodedError); !ok || ce.Code != errors.PULSE_WEIGHT_LOW_NEFF {
		t.Fatalf("strict code: %v", err)
	}
	resp, err = run(types.WeightKindFrequency, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range resp.Warnings {
		if w.Code == string(errors.PULSE_WEIGHT_LOW_NEFF) {
			t.Fatalf("frequency warned: %+v", w)
		}
	}
}
