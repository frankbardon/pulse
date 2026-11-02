package processing

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// The opt-in methodological options of the two-group and contingency
// tests against R (U64 #644), on the gen_unweighted_tests.R goldens
// the default arms are pinned on (test_unweighted_oracle_test.go):
// TEST_T params.variance "pooled" against t.test(var.equal = TRUE),
// TEST_MANN_WHITNEY_U params.exact against R 4.6's wilcox.test(exact
// = TRUE) (ties included), TEST_CHISQ and OVERLAY_CHISQ_MATRIX
// params.correct against chisq.test(correct = TRUE), and TEST_PROP_Z
// params.correct / params.interval "wilson" against prop.test(correct
// = TRUE) and its Wilson interval. Relative 1e-10 (oracleRelTol).

func withParams(t *testing.T, spec *types.Test, params map[string]any) *types.Test {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	spec.Params = raw
	return spec
}

// TestReferenceOracle_TTestPooled: TEST_T params.variance "pooled" on
// every two-sample case (the 1e12 offsets included) — t, df, p, the
// interval and the pooled variance; TEST_WELCH has no such param.
func TestReferenceOracle_TTestPooled(t *testing.T) {
	t.Parallel()

	doc := loadReference[struct {
		tOracleCase
		PooledStatistic float64 `json:"pooled_statistic"`
		PooledDF        float64 `json:"pooled_df"`
		PooledP         float64 `json:"pooled_p_value"`
		PooledCILow     float64 `json:"pooled_ci_low"`
		PooledCIHigh    float64 `json:"pooled_ci_high"`
		PooledVariance  float64 `json:"pooled_variance"`
	}](t, "test_t")
	c := newOracleCheck(t, "TEST_T variance pooled", oracleRelTol, oracleFigureAbsFloor)
	ran := 0
	for _, k := range doc.Cases {
		if k.Variant == "one_sample" {
			continue
		}
		schema := twoSampleFixtureSchema()
		spec := withParams(t, &types.Test{Type: types.TEST_T, Field: "revenue", SplitBy: "treatment", Alpha: k.Alpha}, map[string]any{"variance": "pooled"})
		res := runRegisteredRow(t, spec, schema, feedTwoGroups(t, schema, k.A, k.B))
		if res.Variant != "pooled_two_sample" {
			t.Errorf("%s: variant %q, want pooled_two_sample", k.Name, res.Variant)
		}
		c.check(res.Statistic, k.PooledStatistic, "%s statistic", k.Name)
		c.check(res.DF, k.PooledDF, "%s df", k.Name)
		c.check(res.PValue, k.PooledP, "%s p_value", k.Name)
		c.check(detailFloat(t, res.Details, "ci_low"), k.PooledCILow, "%s ci_low", k.Name)
		c.check(detailFloat(t, res.Details, "ci_high"), k.PooledCIHigh, "%s ci_high", k.Name)
		c.check(detailFloat(t, res.Details, "pooled_variance"), k.PooledVariance, "%s pooled_variance", k.Name)
		c.check(detailFloat(t, res.Details, "diff"), k.Diff, "%s diff", k.Name)
		ran++
	}
	c.done(ran)
}

// TestReferenceOracle_MannWhitneyExact: TEST_MANN_WHITNEY_U params.exact
// against wilcox.test(exact = TRUE) — untied (pwilcox) and tied
// (R 4.6's permutation distribution on mid-ranks) samples alike; a case
// past the exact cap keeps the approximation's p and warns
// PULSE_TEST_EXACT_UNAVAILABLE.
func TestReferenceOracle_MannWhitneyExact(t *testing.T) {
	t.Parallel()

	doc := loadReference[struct {
		Name   string    `json:"name"`
		A      []float64 `json:"a"`
		B      []float64 `json:"b"`
		PValue float64   `json:"p_value"`
		UMin   float64   `json:"u_min"`
		Exact  *float64  `json:"exact_p_value"`
		RExact *float64  `json:"r_p_exact"`
	}](t, "test_mann_whitney_u")
	c := newOracleCheck(t, "TEST_MANN_WHITNEY_U exact", oracleRelTol, oracleProbAbsFloor)
	exact, capped := 0, 0
	for _, k := range doc.Cases {
		schema := twoSampleFixtureSchema()
		spec := withParams(t, &types.Test{Type: types.TEST_MANN_WHITNEY_U, Field: "revenue", SplitBy: "treatment"}, map[string]any{"exact": true})
		res := runRegisteredRow(t, spec, schema, feedTwoGroups(t, schema, k.A, k.B))
		c.check(res.Statistic, k.UMin, "%s statistic", k.Name)
		warned := false
		for _, w := range res.Warnings {
			warned = warned || strings.HasPrefix(w, "PULSE_TEST_EXACT_UNAVAILABLE")
		}
		if k.Exact == nil {
			capped++
			if res.Variant != "asymptotic" || !warned {
				t.Errorf("%s: past the exact cap: variant %q, warnings %v; want asymptotic + PULSE_TEST_EXACT_UNAVAILABLE", k.Name, res.Variant, res.Warnings)
			}
			c.check(res.PValue, k.PValue, "%s approximation p", k.Name)
			continue
		}
		exact++
		if res.Variant != "exact" || warned {
			t.Errorf("%s: variant %q, warnings %v; want exact, no cap warning", k.Name, res.Variant, res.Warnings)
		}
		c.check(res.PValue, *k.Exact, "%s exact p", k.Name)
		if k.RExact != nil && *k.RExact != *k.Exact {
			t.Errorf("%s: r_p_exact %v differs from exact_p_value %v", k.Name, *k.RExact, *k.Exact)
		}
	}
	if exact == 0 || capped == 0 {
		t.Fatalf("golden covers %d exact and %d capped cases; want both arms", exact, capped)
	}
	c.done(exact + capped)
}

// chiSqTableFeed replays a contingency table as one record per counted
// unit (levels r1.., c1..).
func chiSqTableFeed(t *testing.T, schema *encoding.Schema, table [][]float64) func(RowTest) {
	return func(rt RowTest) {
		for i, row := range table {
			for j, n := range row {
				for range int(n) {
					rec := NewRecord(schema, map[string]float64{
						"r": float64(dictIDOrAdd(schema, "r", "r"+strconv.Itoa(i+1))),
						"c": float64(dictIDOrAdd(schema, "c", "c"+strconv.Itoa(j+1))),
					})
					if err := rt.UpdateRow(rec); err != nil {
						t.Fatalf("UpdateRow: %v", err)
					}
				}
			}
		}
	}
}

// TestReferenceOracle_ChiSqYates: params.correct on TEST_CHISQ and on
// OVERLAY_CHISQ_MATRIX against chisq.test(correct = TRUE) on every
// 2 × 2 table (Yates' term min(0.5, |O − E|) included); inert on the
// larger tables, where both keep chisq.test(correct = FALSE)'s figure
// and report no continuity_correction.
func TestReferenceOracle_ChiSqYates(t *testing.T) {
	t.Parallel()

	doc := loadReference[struct {
		Name       string      `json:"name"`
		Variant    string      `json:"variant"`
		Table      [][]float64 `json:"table"`
		Statistic  float64     `json:"statistic"`
		PValue     float64     `json:"p_value"`
		YatesStat  *float64    `json:"r_yates_statistic"`
		YatesP     *float64    `json:"r_yates_p_value"`
		ExpMinimum float64     `json:"expected_min"`
	}](t, "test_chisq")
	c := newOracleCheck(t, "TEST_CHISQ / OVERLAY_CHISQ_MATRIX correct", oracleRelTol, oracleProbAbsFloor)
	twoByTwo, larger := 0, 0
	for _, k := range doc.Cases {
		if k.Variant != "independence" {
			continue
		}
		wantStat, wantP := k.Statistic, k.PValue
		if k.YatesStat != nil {
			wantStat, wantP = *k.YatesStat, *k.YatesP
			twoByTwo++
		} else {
			larger++
		}
		schema := &encoding.Schema{Fields: []encoding.Field{
			{Name: "r", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
			{Name: "c", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
		}}
		spec := withParams(t, &types.Test{Type: types.TEST_CHISQ, Rows: "r", Cols: "c"}, map[string]any{"correct": true})
		res := runRegisteredRow(t, spec, schema, chiSqTableFeed(t, schema, k.Table))
		c.check(res.Statistic, wantStat, "%s TEST_CHISQ statistic", k.Name)
		c.check(res.PValue, wantP, "%s TEST_CHISQ p", k.Name)
		_, applied := res.Details["continuity_correction"]
		if applied != (k.YatesStat != nil) {
			t.Errorf("%s: continuity_correction present %v, want %v", k.Name, applied, k.YatesStat != nil)
		}

		ov := types.OverlaySpec{Kind: types.OverlayKindChiSqMatrix, Scope: types.OverlayScopeMatrix, Params: json.RawMessage(`{"correct":true}`)}
		layers, _, err := applyOverlays([]types.OverlaySpec{ov}, NewCrosstabHostView(countHostPayload(k.Table)))
		if err != nil {
			t.Fatalf("%s: applyOverlays: %v", k.Name, err)
		}
		s := layers[0].Summary
		c.check(*s.Statistic, wantStat, "%s overlay statistic", k.Name)
		c.check(*s.PValue, wantP, "%s overlay p", k.Name)
		if _, ok := s.Parameters["continuity_correction"]; ok != (k.YatesStat != nil) {
			t.Errorf("%s: overlay continuity_correction present %v, want %v", k.Name, ok, k.YatesStat != nil)
		}
	}
	if twoByTwo < 3 || larger == 0 {
		t.Fatalf("golden covers %d 2 x 2 and %d larger tables", twoByTwo, larger)
	}
	c.done(twoByTwo + larger)
}

// TestReferenceOracle_PropZOptions: TEST_PROP_Z params.correct against
// prop.test(correct = TRUE) (both forms: z = sign·√X², p, and the
// interval — two-proportion Wald widened by YATES·Σ1/n, one-proportion
// Pulse's Wald on p − p0 widened by YATES/n, evaluated in R), and
// params.interval "wilson" against prop.test's Wilson bounds, without
// and with the correction, on p − p0.
func TestReferenceOracle_PropZOptions(t *testing.T) {
	t.Parallel()

	doc := loadReference[struct {
		Name        string          `json:"name"`
		Variant     string          `json:"variant"`
		Successes   json.RawMessage `json:"successes"`
		N           json.RawMessage `json:"n"`
		P0          float64         `json:"p0"`
		Alpha       float64         `json:"alpha"`
		YStatistic  float64         `json:"yates_statistic"`
		YPValue     float64         `json:"yates_p_value"`
		YCILow      float64         `json:"yates_ci_low"`
		YCIHigh     float64         `json:"yates_ci_high"`
		WilsonLow   *float64        `json:"r_wilson_low"`
		WilsonHigh  *float64        `json:"r_wilson_high"`
		YWilsonLow  *float64        `json:"r_yates_wilson_low"`
		YWilsonHigh *float64        `json:"r_yates_wilson_high"`
	}](t, "test_prop_z")
	c := newOracleCheck(t, "TEST_PROP_Z correct / interval", oracleRelTol, oracleProbAbsFloor)
	ran := 0
	for _, k := range doc.Cases {
		one := k.Variant == "one_proportion"
		var successes, ns []float64
		if one {
			var s, n float64
			mustUnmarshal(t, k.Successes, &s)
			mustUnmarshal(t, k.N, &n)
			successes, ns = []float64{s}, []float64{n}
		} else {
			mustUnmarshal(t, k.Successes, &successes)
			mustUnmarshal(t, k.N, &ns)
		}
		run := func(extra map[string]any) *types.TestResult {
			schema := &encoding.Schema{Fields: []encoding.Field{
				{Name: "outcome", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
				{Name: "arm", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
			}}
			dictIDOrAdd(schema, "outcome", "yes")
			dictIDOrAdd(schema, "outcome", "no")
			params := map[string]any{"success": "yes"}
			spec := &types.Test{Type: types.TEST_PROP_Z, Field: "outcome", Alpha: k.Alpha}
			if one {
				params["p0"] = k.P0
			} else {
				spec.SplitBy = "arm"
			}
			for key, v := range extra {
				params[key] = v
			}
			return runRegisteredRow(t, withParams(t, spec, params), schema, func(rt RowTest) {
				for g := range successes {
					for i := range int(ns[g]) {
						outcome := "no"
						if i < int(successes[g]) {
							outcome = "yes"
						}
						vals := map[string]float64{"outcome": float64(dictIDOrAdd(schema, "outcome", outcome))}
						if !one {
							vals["arm"] = float64(dictIDOrAdd(schema, "arm", string(rune('a'+g))))
						}
						if err := rt.UpdateRow(NewRecord(schema, vals)); err != nil {
							t.Fatalf("%s: UpdateRow: %v", k.Name, err)
						}
					}
				}
			})
		}
		res := run(map[string]any{"correct": true})
		c.check(res.Statistic, k.YStatistic, "%s corrected z", k.Name)
		c.check(res.PValue, k.YPValue, "%s corrected p", k.Name)
		c.check(detailFloat(t, res.Details, "ci_low"), k.YCILow, "%s corrected ci_low", k.Name)
		c.check(detailFloat(t, res.Details, "ci_high"), k.YCIHigh, "%s corrected ci_high", k.Name)
		ran++
		if !one {
			continue
		}
		res = run(map[string]any{"interval": "wilson"})
		c.check(detailFloat(t, res.Details, "ci_low"), *k.WilsonLow-k.P0, "%s wilson ci_low", k.Name)
		c.check(detailFloat(t, res.Details, "ci_high"), *k.WilsonHigh-k.P0, "%s wilson ci_high", k.Name)
		res = run(map[string]any{"interval": "wilson", "correct": true})
		c.check(detailFloat(t, res.Details, "ci_low"), *k.YWilsonLow-k.P0, "%s corrected wilson ci_low", k.Name)
		c.check(detailFloat(t, res.Details, "ci_high"), *k.YWilsonHigh-k.P0, "%s corrected wilson ci_high", k.Name)
		c.check(res.Statistic, k.YStatistic, "%s corrected wilson z", k.Name)
	}
	c.done(ran)
}
