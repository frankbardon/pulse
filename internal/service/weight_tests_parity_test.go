package service

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// Test-slot parity harness (weighting-inferential E1-S4): the tier-1
// significance tests through the same arms, stores and weight sources
// as the aggregator harness in weight_parity_test.go.
//
//   - Unity (TestWeightUnityParity/tests): an all-1.0 weight answers
//     BYTE-identically to no weight once each result's sum_weights /
//     n_eff are checked (= n) and shed — every arm the unweighted test
//     takes (buffered, streaming; tests never fan out), every weight
//     source whose kind the manifest advertises for the test.
//   - Expansion (TestWeightFrequencyExpansionParity/tests): kind
//     frequency — integer weights answer like the physically duplicated
//     rows run unweighted; kind probability — the frequency identity
//     does not hold (N* is n_eff, not Σw), so the probability arm is
//     scale invariance instead: weights c·f answer like f, totals ×c.
//
// The row table is held total over the manifest's tests weight_kinds,
// per kind, by the shared coverage helper (weight_coverage_test.go). A
// test flipped to a weight class gets its row here; the kinds it runs
// under come from the manifest, so a frequency-only rank test (E2)
// needs only its row.

// testParityOp is one weight-aware tier-1 test's row.
type testParityOp struct {
	name string
	spec types.Test
	// totals are details keys holding weighted TOTALS (a Σw table, a
	// success mass): they move by c when every weight is scaled by c.
	// sum_weights is always one.
	totals []string
	// raw are details keys holding RAW row counts beside `n` (a total
	// row count, a dropped-row count): they stay raw under a weight, so
	// the expansion arm does not compare them. "warnings" there means a
	// warning's text counts raw rows: the expansion arm then compares
	// the warning codes only.
	raw []string
}

var testParityOps = []testParityOp{
	{name: "t_one_sample", spec: types.Test{Type: types.TEST_T, Field: "x", Params: json.RawMessage(`{"mu":0.5}`)}},
	{name: "t_split", spec: types.Test{Type: types.TEST_T, Field: "x", SplitBy: "h"}},
	{name: "welch", spec: types.Test{Type: types.TEST_WELCH, Field: "x", SplitBy: "h"}},
	{name: "z", spec: types.Test{Type: types.TEST_Z_TWO_SAMPLE, Field: "x", SplitBy: "h"}},
	{name: "paired", spec: types.Test{Type: types.TEST_PAIRED_T, Field: "x", Field2: "y"}},
	{name: "anova_f", spec: types.Test{Type: types.TEST_ANOVA_F, Field: "x", SplitBy: "k"}},
	{name: "anova_welch", spec: types.Test{Type: types.TEST_ANOVA_WELCH, Field: "x", SplitBy: "k"}},
	{name: "pearson", spec: types.Test{Type: types.TEST_PEARSON_R, Field: "x", Field2: "y"}},
	{name: "chisq", spec: types.Test{Type: types.TEST_CHISQ, Rows: "h", Cols: "o"},
		totals: []string{"contingency", "row_totals", "col_totals"}},
	{name: "prop_z", spec: types.Test{Type: types.TEST_PROP_Z, Field: "o", SplitBy: "h", Params: json.RawMessage(`{"success":"yes"}`)},
		totals: []string{"successes"}},
	// Frequency-only rank tests (E2-S1): the probability arm is skipped by
	// the manifest's weight_kinds.
	{name: "mann_whitney", spec: types.Test{Type: types.TEST_MANN_WHITNEY_U, Field: "x", SplitBy: "h"}},
	{name: "wilcoxon_sr", spec: types.Test{Type: types.TEST_WILCOXON_SR, Field: "x", Field2: "y"}, raw: []string{"zero_diffs", "warnings"}},
	{name: "kruskal", spec: types.Test{Type: types.TEST_KRUSKAL_WALLIS, Field: "x", SplitBy: "k"}, raw: []string{"n_total"}},
	{name: "spearman", spec: types.Test{Type: types.TEST_SPEARMAN_R, Field: "x", Field2: "y"}},
	{name: "kendall", spec: types.Test{Type: types.TEST_KENDALL_TAU, Field: "x", Field2: "y"}},
}

// build returns the row's request builder over path.
func (o testParityOp) build(path string) func() *types.Request {
	return func() *types.Request {
		spec := o.spec
		spec.Label = "test_" + o.name
		return &types.Request{Cohort: &types.Cohort{Filename: path}, Tests: []*types.Test{&spec},
			Aggregations: []*types.Aggregation{testSteerCount()}}
	}
}

// testSteerCount is the opted-out AGG_COUNT a tests-only request needs to
// stream at all (the streaming path requires an aggregator); stripSteer
// drops it before comparison.
func testSteerCount() *types.Aggregation {
	return &types.Aggregation{Type: types.AGG_COUNT, Field: "y", Label: paritySteer + "count", Weight: types.NullSlotWeight()}
}

// kinds is the weight kinds the manifest advertises for the row's test.
func (o testParityOp) kinds() []types.WeightKind {
	return manifestWeightKinds(weightSurfaceTests)[string(o.spec.Type)]
}

// sourcesFor filters paritySources to the kinds a row runs under.
func sourcesFor(kinds []types.WeightKind) []paritySource {
	var out []paritySource
	for _, s := range paritySources() {
		if slices.Contains(kinds, s.kind) {
			out = append(out, s)
		}
	}
	return out
}

func assertTestParityCoverage(t *testing.T) {
	t.Helper()
	have := map[string][]types.WeightKind{}
	for _, r := range testParityOps {
		if len(r.kinds()) == 0 {
			t.Errorf("testParityOps row %s: the manifest advertises no weight kind for %s — drop the row or fix the class table", r.name, r.spec.Type)
		}
		for _, k := range r.kinds() {
			if !slices.Contains(have[string(r.spec.Type)], k) {
				have[string(r.spec.Type)] = append(have[string(r.spec.Type)], k)
			}
		}
	}
	assertWeightKindCoverage(t, weightSurfaceTests, have, "testParityOps, weight_tests_parity_test.go")
}

// testUnityParity is TestWeightUnityParity's test-slot half.
func testUnityParity(t *testing.T, store *parityStore) {
	t.Run("coverage", assertTestParityCoverage)
	streamed := 0
	for _, mode := range parityModes() {
		for _, row := range testParityOps {
			t.Run(mode.name+"/"+row.name, func(t *testing.T) {
				path := store.paths[mode.cohort]
				base := runArmRequest(t, store, mode, path, row.build(path), nil)
				if base == nil {
					t.Logf("%s does not run on the %s arm unweighted; not applicable", row.spec.Type, mode.name)
					return
				}
				if mode.name == "streaming" {
					streamed++
				}
				stripSteer(base)
				want := mustMarshal(t, base)
				for _, src := range sourcesFor(row.kinds()) {
					t.Run(src.name, func(t *testing.T) {
						got := runArmRequest(t, store, mode, path, row.build(path), &src)
						stripSteer(got)
						shedTestUnity(t, got, src.kind)
						if g := mustMarshal(t, got); string(g) != string(want) {
							t.Errorf("unity weight changed the answer:\n weighted   %s\n unweighted %s", g, want)
						}
					})
				}
			})
		}
	}
	if streamed == 0 {
		t.Fatal("no test ran on the streaming arm: the streaming half is vacuous")
	}
}

// shedTestUnity asserts each test result's weight keys carry their
// unity values (sum_weights = n; n_eff = n under probability, absent
// under frequency) and removes them.
func shedTestUnity(t *testing.T, resp *types.Response, kind types.WeightKind) {
	t.Helper()
	if len(resp.Tests) == 0 {
		t.Fatal("weighted response has no test results")
	}
	for _, tr := range resp.Tests {
		n := floatsOf(tr.Details["n"])
		sw := floatsOf(tr.Details["sum_weights"])
		if sw == nil {
			t.Fatalf("%s: no sum_weights — the weight did not apply", tr.Label)
		}
		if !slices.Equal(sw, n) {
			t.Errorf("%s: unity sum_weights %v, want n %v", tr.Label, sw, n)
		}
		neff := floatsOf(tr.Details["n_eff"])
		switch {
		case kind == types.WeightKindProbability && !slices.Equal(neff, n):
			t.Errorf("%s: unity n_eff %v, want n %v", tr.Label, neff, n)
		case kind == types.WeightKindFrequency && neff != nil:
			t.Errorf("%s: n_eff %v under kind frequency", tr.Label, neff)
		}
		delete(tr.Details, "sum_weights")
		delete(tr.Details, "n_eff")
	}
}

// floatsOf flattens a scalar or a slice of numbers; nil when v is not
// numeric.
func floatsOf(v any) []float64 {
	switch x := v.(type) {
	case []float64:
		return x
	case []int64:
		out := make([]float64, len(x))
		for i, n := range x {
			out[i] = float64(n)
		}
		return out
	case []int:
		out := make([]float64, len(x))
		for i, n := range x {
			out[i] = float64(n)
		}
		return out
	}
	if f, ok := parityFloat(v); ok {
		return []float64{f}
	}
	return nil
}

// testExpandTol: weighted and expanded runs fold the same moments in a
// different order (one weight-w update vs w unit updates through the
// Welford recurrence), so figures agree to a few ulps, and a p-value
// tail amplifies the statistic's relative error by |t·d ln p/dt| (tens
// at most on this fixture). 1e-10 holds both with margin; the
// aggregator gate's 1e-12 is for figures with no tail.
const testExpandTol = 1e-10

// testScaleC is the probability scale-invariance factor (non-dyadic).
const testScaleC = 0.37

// testExpansionParity is TestWeightFrequencyExpansionParity's test-slot
// half: weighted / expanded share the aggregator gate's stores; scaled
// holds the weighted rows with every weight × testScaleC.
func testExpansionParity(t *testing.T, weighted, expanded, scaled *parityStore) {
	t.Run("coverage", assertTestParityCoverage)
	src := map[types.WeightKind]paritySource{}
	for _, s := range paritySources()[:2] { // request_probability, request_frequency
		src[s.kind] = s
	}
	streamed := 0
	for _, mode := range parityModes() {
		for _, row := range testParityOps {
			t.Run(mode.name+"/"+row.name, func(t *testing.T) {
				for _, kind := range row.kinds() {
					t.Run(string(kind), func(t *testing.T) {
						s := src[kind]
						switch kind {
						case types.WeightKindFrequency:
							base := runArmRequest(t, expanded, mode, expanded.paths[mode.cohort], row.build(expanded.paths[mode.cohort]), nil)
							if base == nil {
								t.Skipf("%s does not run on the %s arm", row.spec.Type, mode.name)
							}
							if mode.name == "streaming" {
								streamed++
							}
							stripSteer(base)
							got := runArmRequest(t, weighted, mode, weighted.paths[mode.cohort], row.build(weighted.paths[mode.cohort]), &s)
							stripSteer(got)
							assertTestsExpanded(t, got, base, row.raw)
						case types.WeightKindProbability:
							if runArmRequest(t, weighted, mode, weighted.paths[mode.cohort], row.build(weighted.paths[mode.cohort]), nil) == nil {
								t.Skipf("%s does not run on the %s arm", row.spec.Type, mode.name)
							}
							base := runArmRequest(t, weighted, mode, weighted.paths[mode.cohort], row.build(weighted.paths[mode.cohort]), &s)
							stripSteer(base)
							got := runArmRequest(t, scaled, mode, scaled.paths[mode.cohort], row.build(scaled.paths[mode.cohort]), &s)
							stripSteer(got)
							assertTestsScaled(t, got, base, row.totals)
						default:
							t.Fatalf("no parity arm for weight kind %q", kind)
						}
					})
				}
			})
		}
	}
	if streamed == 0 {
		t.Fatal("no test expanded on the streaming arm: the streaming half is vacuous")
	}
}

// testsWire returns resp.Tests in wire form.
func testsWire(t *testing.T, resp *types.Response) []map[string]any {
	t.Helper()
	var out []map[string]any
	if err := json.Unmarshal(mustMarshal(t, resp.Tests), &out); err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatal("no test results")
	}
	return out
}

// assertTestsExpanded: every figure of the frequency-weighted result
// matches the expanded run within testExpandTol; the raw `n` (and the
// row's other raw counts) is not compared (it stays the row count) —
// sum_weights equals the expanded n exactly instead, and no n_eff
// appears.
func assertTestsExpanded(t *testing.T, got, want *types.Response, raw []string) {
	t.Helper()
	g, w := testsWire(t, got), testsWire(t, want)
	if len(g) != len(w) {
		t.Fatalf("%d test results, expanded %d", len(g), len(w))
	}
	for i := range w {
		gd, _ := g[i]["details"].(map[string]any)
		wd, _ := w[i]["details"].(map[string]any)
		if gd == nil || gd["sum_weights"] == nil {
			t.Fatalf("result %d: no sum_weights — the weight did not apply", i)
		}
		if gd["n_eff"] != nil {
			t.Errorf("result %d: n_eff under kind frequency", i)
		}
		if gs, wn := wireFloats(gd["sum_weights"]), wireFloats(wd["n"]); !slices.Equal(gs, wn) {
			t.Errorf("result %d: sum_weights %v, expanded n %v", i, gs, wn)
		}
		skip := map[string]bool{"n": true, "sum_weights": true}
		for _, k := range raw {
			skip[k] = true
		}
		compareTestWire(t, fmt.Sprintf("result %d", i), g[i], w[i], 1, 1, nil, skip)
		if skip["warnings"] {
			if gc, wc := warningCodes(g[i]["warnings"]), warningCodes(w[i]["warnings"]); !slices.Equal(gc, wc) {
				t.Errorf("result %d: warning codes %v, expanded %v", i, gc, wc)
			}
		}
	}
}

// assertTestsScaled: weights × c under kind probability answer the
// same figures (n_eff and every Kish-scaled statistic are scale-free);
// sum_weights and the row's totals are × c.
func assertTestsScaled(t *testing.T, got, want *types.Response, totals []string) {
	t.Helper()
	g, w := testsWire(t, got), testsWire(t, want)
	if len(g) != len(w) {
		t.Fatalf("%d test results, unscaled %d", len(g), len(w))
	}
	tot := map[string]bool{"sum_weights": true}
	for _, k := range totals {
		tot[k] = true
	}
	for i := range w {
		if d, _ := g[i]["details"].(map[string]any); d == nil || d["n_eff"] == nil {
			t.Fatalf("result %d: no n_eff — the probability weight did not apply", i)
		}
		compareTestWire(t, fmt.Sprintf("result %d", i), g[i], w[i], 1, testScaleC, tot, nil)
	}
}

// compareTestWire walks two wire values: numbers within testExpandTol
// of want × factor, everything else exactly. A key in totals (at any
// depth) switches its subtree's factor to c; a key in skip is not
// compared; the key sets must match otherwise.
func compareTestWire(t *testing.T, where string, got, want any, factor, c float64, totals, skip map[string]bool) {
	t.Helper()
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			t.Errorf("%s: %T, want an object", where, got)
			return
		}
		keys := map[string]bool{}
		for k := range w {
			keys[k] = true
		}
		for k := range g {
			keys[k] = true
		}
		for _, k := range sortedNames(keys) {
			if skip[k] {
				continue
			}
			f := factor
			if totals[k] {
				f = c
			}
			compareTestWire(t, where+"."+k, g[k], w[k], f, c, totals, skip)
		}
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			t.Errorf("%s = %v, want %v", where, got, want)
			return
		}
		for i := range w {
			compareTestWire(t, fmt.Sprintf("%s[%d]", where, i), g[i], w[i], factor, c, totals, skip)
		}
	case float64:
		g, ok := got.(float64)
		if !ok {
			t.Errorf("%s = %v (%T), want %v", where, got, got, want)
			return
		}
		if !relClose(g, w*factor, testExpandTol) {
			t.Errorf("%s = %.17g, want %.17g (rel %.3g)", where, g, w*factor, math.Abs(g-w*factor)/math.Abs(w*factor))
		}
	default:
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s = %v, want %v", where, got, want)
		}
	}
}

// warningCodes is the code prefix ("CODE: text") of each wire warning.
func warningCodes(v any) []string {
	ws, _ := v.([]any)
	out := make([]string, 0, len(ws))
	for _, w := range ws {
		s, _ := w.(string)
		code, _, _ := strings.Cut(s, ":")
		out = append(out, code)
	}
	return out
}

func wireFloats(v any) []float64 {
	switch x := v.(type) {
	case float64:
		return []float64{x}
	case []any:
		out := make([]float64, 0, len(x))
		for _, e := range x {
			f, ok := e.(float64)
			if !ok {
				return nil
			}
			out = append(out, f)
		}
		return out
	}
	return nil
}

func relClose(got, want, tol float64) bool {
	if got == want {
		return true
	}
	if want == 0 {
		return math.Abs(got) <= tol
	}
	return math.Abs(got-want) <= tol*math.Abs(want)
}
