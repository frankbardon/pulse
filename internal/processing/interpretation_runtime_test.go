package processing

// INTERPRETATION HONESTY (tests).
//
// An Interpretation explains how to read one output field of an
// inferential operator. internal/descriptor validates the path
// statically against the result structs, but a map-valued path
// (details.*) names a key that exists only at run time — the static
// resolver can only DEFER it (descx.DeclaredInterpretationFields marks
// those pairs Deferred). This file is where a deferred path meets a real
// emitted value: it RUNS the test through its registered factory on a
// fixture and requires the key in the Go value (res.Details), not in
// some JSON rendering of it.
//
// It also holds the declared effect sizes (descx.EffectSizeKeysByTest)
// two-way against the factories: every registered row / post test runs
// at least one fixture, the union of the keys under
// Details["effect_size"] per (family, tier) must EQUAL the declaration —
// a declared key no fixture emits fails, and so does an emitted key the
// declaration does not name. Conditional key sets get a fixture each:
// TEST_CHISQ on 2x2 (phi) and 2x3, TEST_T one- and two-sample.
//
// Completeness runs both ways for both halves: a registered test with no
// fixture fails, a fixture for an unregistered test fails, a deferred
// declaration with no probe fails, and a probe whose declaration is gone
// fails. Overlay paths (summary.parameters.*) are the internal/service
// twin's job (TestOverlayInterpretationFieldsHoldAtRuntime).

import (
	"encoding/json"
	"math"
	"sort"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	descx "github.com/frankbardon/pulse/internal/descriptor"
	"github.com/frankbardon/pulse/types"
)

const (
	tierRow  = "row"
	tierPost = "post"
)

// testProbeFixture runs one registered test (family + tier) on fixed
// data through its registry factory.
type testProbeFixture struct {
	family types.TestType
	tier   string
	label  string
	run    func(t *testing.T) *types.TestResult
}

// interpretationProbes lists, per family, the deferred Interpretation
// fields this file proves emitted. Held two-way against
// descx.DeclaredInterpretationFields: add a deferred declaration and it
// must land here; drop one and its probe must go.
var interpretationProbes = map[string][]string{
	"TEST_ANOVA_F":   {"details.effect_size.eta_squared", "details.effect_size.omega_squared"},
	"TEST_PEARSON_R": {"details.ci_high", "details.ci_low"},
}

// runRegisteredRow constructs typ's row test from the registry and feeds
// it the records build produces.
func runRegisteredRow(t *testing.T, spec *types.Test, schema *encoding.Schema, feed func(rt RowTest)) *types.TestResult {
	t.Helper()
	factory, ok := rowTestRegistry[spec.Type]
	if !ok {
		t.Fatalf("%s is not a registered row test", spec.Type)
	}
	rt, err := factory(spec, schema)
	if err != nil {
		t.Fatalf("%s row factory: %v", spec.Type, err)
	}
	feed(rt)
	res, err := rt.Finalize()
	if err != nil {
		t.Fatalf("%s Finalize: %v", spec.Type, err)
	}
	return res
}

func runRegisteredPost(t *testing.T, spec *types.Test, rows []map[string]any) *types.TestResult {
	t.Helper()
	factory, ok := postTestRegistry[spec.Type]
	if !ok {
		t.Fatalf("%s is not a registered post test", spec.Type)
	}
	pt, err := factory(spec, nil)
	if err != nil {
		t.Fatalf("%s post factory: %v", spec.Type, err)
	}
	res, err := pt.Run(rows)
	if err != nil {
		t.Fatalf("%s Run: %v", spec.Type, err)
	}
	return res
}

func mustUpdate(t *testing.T, rt RowTest, r *Record) {
	t.Helper()
	if err := rt.UpdateRow(r); err != nil {
		t.Fatalf("UpdateRow: %v", err)
	}
}

// groupedRow runs a revenue-by-treatment row test over groups.
func groupedRow(typ types.TestType) func(t *testing.T) *types.TestResult {
	groups := plantGrowth
	switch typ {
	case types.TEST_T, types.TEST_WELCH, types.TEST_KS, types.TEST_MANN_WHITNEY_U, types.TEST_Z_TWO_SAMPLE:
		groups = sleepGroups
	}
	return func(t *testing.T) *types.TestResult {
		schema := twoSampleFixtureSchema()
		return runRegisteredRow(t, &types.Test{Type: typ, Field: "revenue", SplitBy: "treatment"}, schema, func(rt RowTest) {
			for _, g := range groups {
				for _, v := range g.values {
					mustUpdate(t, rt, newTestRecord(t, schema, map[string]float64{"revenue": v}, "treatment", g.name))
				}
			}
		})
	}
}

// pairedXY is R sleep (group 2 as x, group 1 as y): non-constant
// differences, one zero difference, no perfect correlation.
var pairedXY = [][2]float64{
	{1.9, 0.7}, {0.8, -1.6}, {1.1, -0.2}, {0.1, -1.2}, {-0.1, -0.1},
	{4.4, 3.4}, {5.5, 3.7}, {1.6, 0.8}, {4.6, 0.0}, {3.4, 2.0},
}

func pairedRow(typ types.TestType) func(t *testing.T) *types.TestResult {
	return func(t *testing.T) *types.TestResult {
		schema := &encoding.Schema{Fields: []encoding.Field{
			{Name: "x", Type: encoding.FieldTypeF64},
			{Name: "y", Type: encoding.FieldTypeF64},
		}}
		return runRegisteredRow(t, &types.Test{Type: typ, Field: "x", Field2: "y"}, schema, func(rt RowTest) {
			for _, p := range pairedXY {
				mustUpdate(t, rt, NewRecord(schema, map[string]float64{"x": p[0], "y": p[1]}))
			}
		})
	}
}

func pairedPost(typ types.TestType) func(t *testing.T) *types.TestResult {
	return func(t *testing.T) *types.TestResult {
		rows := make([]map[string]any, len(pairedXY))
		for i, p := range pairedXY {
			rows[i] = map[string]any{"x": p[0], "y": p[1]}
		}
		return runRegisteredPost(t, &types.Test{Type: typ, Field: "x", Field2: "y"}, rows)
	}
}

func contingencyRow(typ types.TestType, table [][]int) func(t *testing.T) *types.TestResult {
	return func(t *testing.T) *types.TestResult {
		schema := chiSquareFixtureSchema()
		rowNames, colNames := []string{"r0", "r1", "r2"}, []string{"c0", "c1", "c2"}
		return runRegisteredRow(t, &types.Test{Type: typ, Rows: "kind", Cols: "outcome"}, schema, func(rt RowTest) {
			for ri, row := range table {
				for ci, count := range row {
					for range count {
						mustUpdate(t, rt, chiSquareTestRecord(t, schema, rowNames[ri], colNames[ci]))
					}
				}
			}
		})
	}
}

// oneSampleValues is PlantGrowth ctrl + trt1: 20 roughly normal values.
func oneSampleValues() []float64 {
	return append(append([]float64(nil), plantGrowth[0].values...), plantGrowth[1].values...)
}

func oneSampleRow(typ types.TestType, params json.RawMessage) func(t *testing.T) *types.TestResult {
	return func(t *testing.T) *types.TestResult {
		schema := numericFixtureSchema("x")
		return runRegisteredRow(t, &types.Test{Type: typ, Field: "x", Params: params}, schema, func(rt RowTest) {
			for _, v := range oneSampleValues() {
				mustUpdate(t, rt, NewRecord(schema, map[string]float64{"x": v}))
			}
		})
	}
}

func groupedPostRows(groups []anovaGroup) []map[string]any {
	var rows []map[string]any
	for _, g := range groups {
		for _, v := range g.values {
			rows = append(rows, map[string]any{"g": g.name, "x": v})
		}
	}
	return rows
}

var summaryPostParams = json.RawMessage(`{"n_col":"n","variance_col":"var_x"}`)

// testProbeFixtures covers every registered (test, tier) pair — the
// completeness subtest enforces it.
var testProbeFixtures = []testProbeFixture{
	// Tier 1 (row).
	{types.TEST_T, tierRow, "one_sample", oneSampleRow(types.TEST_T, json.RawMessage(`{"mu":5}`))},
	{types.TEST_T, tierRow, "two_sample", groupedRow(types.TEST_T)},
	{types.TEST_WELCH, tierRow, "two_sample", groupedRow(types.TEST_WELCH)},
	{types.TEST_Z_TWO_SAMPLE, tierRow, "two_sample", groupedRow(types.TEST_Z_TWO_SAMPLE)},
	{types.TEST_KS, tierRow, "two_sample", groupedRow(types.TEST_KS)},
	{types.TEST_MANN_WHITNEY_U, tierRow, "two_sample", groupedRow(types.TEST_MANN_WHITNEY_U)},
	{types.TEST_ANOVA_F, tierRow, "plant_growth", groupedRow(types.TEST_ANOVA_F)},
	{types.TEST_ANOVA_WELCH, tierRow, "plant_growth", groupedRow(types.TEST_ANOVA_WELCH)},
	{types.TEST_KRUSKAL_WALLIS, tierRow, "plant_growth", groupedRow(types.TEST_KRUSKAL_WALLIS)},
	{types.TEST_BROWN_FORSYTHE, tierRow, "plant_growth", groupedRow(types.TEST_BROWN_FORSYTHE)},
	{types.TEST_PAIRED_T, tierRow, "sleep_pairs", pairedRow(types.TEST_PAIRED_T)},
	{types.TEST_WILCOXON_SR, tierRow, "sleep_pairs", pairedRow(types.TEST_WILCOXON_SR)},
	{types.TEST_PEARSON_R, tierRow, "sleep_pairs", pairedRow(types.TEST_PEARSON_R)},
	{types.TEST_SPEARMAN_R, tierRow, "sleep_pairs", pairedRow(types.TEST_SPEARMAN_R)},
	{types.TEST_KENDALL_TAU, tierRow, "sleep_pairs", pairedRow(types.TEST_KENDALL_TAU)},
	{types.TEST_CHISQ, tierRow, "2x2", contingencyRow(types.TEST_CHISQ, [][]int{{90, 10}, {80, 20}})},
	{types.TEST_CHISQ, tierRow, "2x3", contingencyRow(types.TEST_CHISQ, [][]int{{762, 327, 468}, {484, 239, 477}})},
	{types.TEST_FISHER_EXACT, tierRow, "2x2", contingencyRow(types.TEST_FISHER_EXACT, [][]int{{1, 9}, {11, 3}})},
	{types.TEST_SHAPIRO_WILK, tierRow, "plant_growth", oneSampleRow(types.TEST_SHAPIRO_WILK, nil)},
	{types.TEST_PROP_Z, tierRow, "150of200_vs_170of200", func(t *testing.T) *types.TestResult {
		schema := &encoding.Schema{Fields: []encoding.Field{
			{Name: "treatment", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
			{Name: "converted", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
		}}
		spec := &types.Test{Type: types.TEST_PROP_Z, Field: "converted", SplitBy: "treatment", Params: json.RawMessage(`{"success":"yes"}`)}
		return runRegisteredRow(t, spec, schema, func(rt RowTest) {
			for _, c := range []struct {
				g, o string
				n    int
			}{{"a", "yes", 150}, {"a", "no", 50}, {"b", "yes", 170}, {"b", "no", 30}} {
				g, o := dictIDOrAdd(schema, "treatment", c.g), dictIDOrAdd(schema, "converted", c.o)
				for range c.n {
					mustUpdate(t, rt, NewRecord(schema, map[string]float64{"treatment": float64(g), "converted": float64(o)}))
				}
			}
		})
	}},
	{types.TEST_ANOVA_RM, tierRow, "5x3", func(t *testing.T) *types.TestResult {
		schema := &encoding.Schema{Fields: []encoding.Field{
			{Name: "metric", Type: encoding.FieldTypeF64},
			{Name: "condition", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
			{Name: "subject_id", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
		}}
		spec := &types.Test{Type: types.TEST_ANOVA_RM, Field: "metric", SplitBy: "condition", SubjectField: "subject_id"}
		table := [][]float64{{45, 50, 55}, {42, 42, 45}, {36, 41, 43}, {39, 35, 40}, {51, 55, 59}}
		return runRegisteredRow(t, spec, schema, func(rt RowTest) {
			for i, row := range table {
				for j, v := range row {
					mustUpdate(t, rt, NewRecord(schema, map[string]float64{
						"metric":     v,
						"condition":  float64(dictIDOrAdd(schema, "condition", []string{"a", "b", "c"}[j])),
						"subject_id": float64(dictIDOrAdd(schema, "subject_id", []string{"s1", "s2", "s3", "s4", "s5"}[i])),
					}))
				}
			}
		})
	}},

	// Tier 2 (post).
	{types.TEST_ANOVA_F, tierPost, "plant_growth_summary", func(t *testing.T) *types.TestResult {
		return runRegisteredPost(t, &types.Test{Type: types.TEST_ANOVA_F, Field: "mean_x", SplitBy: "group", Params: summaryPostParams}, summaryRows(plantGrowth))
	}},
	{types.TEST_ANOVA_WELCH, tierPost, "plant_growth_summary", func(t *testing.T) *types.TestResult {
		return runRegisteredPost(t, &types.Test{Type: types.TEST_ANOVA_WELCH, Field: "mean_x", SplitBy: "group", Params: summaryPostParams}, summaryRows(plantGrowth))
	}},
	{types.TEST_PAIRED_T, tierPost, "sleep_pairs", pairedPost(types.TEST_PAIRED_T)},
	{types.TEST_WILCOXON_SR, tierPost, "sleep_pairs", pairedPost(types.TEST_WILCOXON_SR)},
	{types.TEST_PEARSON_R, tierPost, "sleep_pairs", pairedPost(types.TEST_PEARSON_R)},
	{types.TEST_SPEARMAN_R, tierPost, "sleep_pairs", pairedPost(types.TEST_SPEARMAN_R)},
	{types.TEST_KENDALL_TAU, tierPost, "sleep_pairs", pairedPost(types.TEST_KENDALL_TAU)},
	{types.TEST_BROWN_FORSYTHE, tierPost, "plant_growth", func(t *testing.T) *types.TestResult {
		return runRegisteredPost(t, &types.Test{Type: types.TEST_BROWN_FORSYTHE, Field: "x", SplitBy: "g"}, groupedPostRows(plantGrowth))
	}},
	{types.TEST_KS, tierPost, "sleep_groups", func(t *testing.T) *types.TestResult {
		return runRegisteredPost(t, &types.Test{Type: types.TEST_KS, Field: "x", SplitBy: "g"}, groupedPostRows(sleepGroups))
	}},
	{types.TEST_SHAPIRO_WILK, tierPost, "plant_growth", func(t *testing.T) *types.TestResult {
		var rows []map[string]any
		for _, v := range oneSampleValues() {
			rows = append(rows, map[string]any{"x": v})
		}
		return runRegisteredPost(t, &types.Test{Type: types.TEST_SHAPIRO_WILK, Field: "x"}, rows)
	}},
	{types.TEST_TREND, tierPost, "noisy_rise", func(t *testing.T) *types.TestResult {
		vals := []float64{1.0, 2.5, 2.0, 3.5, 4.0, 3.8, 5.2, 6.0}
		rows := make([]map[string]any, len(vals))
		for i, v := range vals {
			rows[i] = map[string]any{"period": float64(i + 1), "value": v}
		}
		return runRegisteredPost(t, &types.Test{Type: types.TEST_TREND, Field: "value", OrderBy: []types.OrderKey{{Field: "period"}}}, rows)
	}},
	{types.TEST_TUKEY_HSD, tierPost, "three_means", func(t *testing.T) *types.TestResult {
		params, _ := json.Marshal(map[string]any{"ms_within": 4.0, "df_within": 27.0})
		return runRegisteredPost(t, &types.Test{Type: types.TEST_TUKEY_HSD, Field: "mean", SplitBy: "group", Params: params}, []map[string]any{
			{"group": "a", "mean": 10.0, "n": 10.0},
			{"group": "b", "mean": 15.0, "n": 10.0},
			{"group": "c", "mean": 20.0, "n": 10.0},
		})
	}},
}

// registeredTestTiers returns every registered (family, tier) pair.
func registeredTestTiers() map[types.TestType]map[string]bool {
	out := map[types.TestType]map[string]bool{}
	add := func(typ types.TestType, tier string) {
		if out[typ] == nil {
			out[typ] = map[string]bool{}
		}
		out[typ][tier] = true
	}
	for typ := range rowTestRegistry {
		add(typ, tierRow)
	}
	for typ := range postTestRegistry {
		add(typ, tierPost)
	}
	return out
}

// detailsPath resolves a details.<a>.<b>... path against the Go value of
// res.Details, descending through nested map[string]any. It reports
// whether the final key is PRESENT (presence, not value sanity — a
// collapsed CI equal to r still counts).
func detailsPath(res *types.TestResult, field string) bool {
	rest, ok := strings.CutPrefix(field, "details.")
	if !ok || res == nil {
		return false
	}
	var cur any = res.Details
	for _, seg := range strings.Split(rest, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return false
		}
		if cur, ok = m[seg]; !ok {
			return false
		}
	}
	return true
}

// fixtureResults runs every fixture once, keyed by family then tier.
func fixtureResults(t *testing.T) map[types.TestType]map[string][]*types.TestResult {
	t.Helper()
	out := map[types.TestType]map[string][]*types.TestResult{}
	for _, f := range testProbeFixtures {
		var res *types.TestResult
		t.Run("fixture/"+string(f.family)+"/"+f.tier+"/"+f.label, func(t *testing.T) {
			res = f.run(t)
		})
		if res == nil {
			continue
		}
		if out[f.family] == nil {
			out[f.family] = map[string][]*types.TestResult{}
		}
		out[f.family][f.tier] = append(out[f.family][f.tier], res)
	}
	return out
}

// TestTestProbeFixturesCoverRegistries: the fixture table runs every
// registered row and post test, and nothing else — the effect-size
// probe's "emitted ⊆ declared" half is only total if every factory runs.
func TestTestProbeFixturesCoverRegistries(t *testing.T) {
	registered := registeredTestTiers()
	have := map[types.TestType]map[string]bool{}
	for _, f := range testProbeFixtures {
		if have[f.family] == nil {
			have[f.family] = map[string]bool{}
		}
		have[f.family][f.tier] = true
		if !registered[f.family][f.tier] {
			t.Errorf("fixture %s/%s/%s targets an unregistered %s test; drop it", f.family, f.tier, f.label, f.tier)
		}
	}
	for typ, tiers := range registered {
		for tier := range tiers {
			if !have[typ][tier] {
				t.Errorf("%s is a registered %s test with no fixture in testProbeFixtures; add one", typ, tier)
			}
		}
	}
}

// TestInterpretationFieldsHoldAtRuntime proves every deferred (map-
// valued) Interpretation field on a test family is really emitted, on
// every tier the family is registered in.
func TestInterpretationFieldsHoldAtRuntime(t *testing.T) {
	declared := map[string]map[string]bool{}
	for _, d := range descx.DeclaredInterpretationFields() {
		if !d.Deferred || d.Category == "overlay" {
			continue // static paths are descriptor's job; overlays are internal/service's
		}
		if declared[d.Name] == nil {
			declared[d.Name] = map[string]bool{}
		}
		declared[d.Name][d.Field] = true
		if d.Category != "test" {
			t.Errorf("%s declares deferred field %q in category %q, which has no runtime fixture harness here; "+
				"add one before declaring a map-valued path on it", d.Name, d.Field, d.Category)
		}
	}
	probed := map[string]map[string]bool{}
	for name, fields := range interpretationProbes {
		probed[name] = map[string]bool{}
		for _, f := range fields {
			probed[name][f] = true
		}
	}

	t.Run("every_declaration_is_probed", func(t *testing.T) {
		for name, fields := range declared {
			for f := range fields {
				if !probed[name][f] {
					t.Errorf("%s declares deferred Interpretation field %q with no probe in interpretationProbes — "+
						"only a runtime probe can show a details.* key exists; add it", name, f)
				}
			}
		}
	})

	t.Run("no_probe_outlives_its_declaration", func(t *testing.T) {
		for name, fields := range probed {
			for f := range fields {
				if !declared[name][f] {
					t.Errorf("interpretationProbes probes %s %q, which is not a declared deferred Interpretation field; "+
						"drop the probe or restore the declaration", name, f)
				}
			}
		}
	})

	results := fixtureResults(t)
	tiers := registeredTestTiers()
	names := make([]string, 0, len(probed))
	for name := range probed {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		family := types.TestType(name)
		for _, field := range interpretationProbes[name] {
			t.Run(name+"/"+field, func(t *testing.T) {
				if len(tiers[family]) == 0 {
					t.Fatalf("%s is not a registered test family", name)
				}
				for tier := range tiers[family] {
					found := false
					for _, res := range results[family][tier] {
						if detailsPath(res, field) {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("%s (%s tier) declares an Interpretation for %q, but no fixture emitted it (ran %d)",
							name, tier, field, len(results[family][tier]))
					}
				}
			})
		}
	}
}

// TestEffectSizeKeysHoldAtRuntime holds descx.EffectSizeKeysByTest
// two-way against the factories, per registered (family, tier): the
// union of emitted Details["effect_size"] keys must equal the
// declaration, and every emitted value must be a finite float64.
func TestEffectSizeKeysHoldAtRuntime(t *testing.T) {
	declared := descx.EffectSizeKeysByTest()
	tiers := registeredTestTiers()
	for fam := range declared {
		if len(tiers[types.TestType(fam)]) == 0 {
			t.Errorf("EffectSizeKeysByTest declares %s, which is not a registered test family", fam)
		}
	}
	results := fixtureResults(t)

	fams := make([]string, 0, len(tiers))
	for typ := range tiers {
		fams = append(fams, string(typ))
	}
	sort.Strings(fams)
	for _, fam := range fams {
		want := map[string]bool{}
		for _, k := range declared[fam] {
			want[k] = true
		}
		for tier := range tiers[types.TestType(fam)] {
			t.Run(fam+"/"+tier, func(t *testing.T) {
				got := map[string]bool{}
				for _, res := range results[types.TestType(fam)][tier] {
					raw, has := res.Details[effectSizeDetailsKey]
					if !has {
						continue
					}
					es, ok := raw.(map[string]any)
					if !ok {
						t.Fatalf("Details[%q] = %T, want map[string]any", effectSizeDetailsKey, raw)
					}
					for k, v := range es {
						got[k] = true
						if f, ok := v.(float64); !ok || math.IsNaN(f) || math.IsInf(f, 0) {
							t.Errorf("effect_size.%s = %#v, want a finite float64", k, v)
						}
					}
				}
				for k := range want {
					if !got[k] {
						t.Errorf("%s declares effect size %q, but no %s-tier fixture emitted it", fam, k, tier)
					}
				}
				for k := range got {
					if !want[k] {
						t.Errorf("%s (%s tier) emits effect size %q, which EffectSizeKeysByTest does not declare", fam, tier, k)
					}
				}
			})
		}
	}
}
