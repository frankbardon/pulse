package processing

import (
	"encoding/json"
	"fmt"
	"strconv"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// Unweighted two-group and contingency tests against R (U64 E2-S1,
// #616): TEST_T, TEST_WELCH, TEST_Z_TWO_SAMPLE, TEST_CHISQ,
// TEST_MANN_WHITNEY_U, TEST_KRUSKAL_WALLIS and TEST_PROP_Z. The goldens
// are written by scripts/reference/gen_unweighted_tests.R (`make
// reference`); each case carries its inputs at %.17g and the operator
// replays them through its registered RowTest (rowTestRegistry: the
// factory a request's tests[] slot builds), at the shared relative 1e-10
// (oracleRelTol). Where Pulse's definition differs from R's default, the
// generator makes R compute Pulse's definition and says so in its
// header: no Yates correction, Mann-Whitney's normal approximation
// (R's default exact p is kept as r_p_exact), and the one-proportion
// Wald interval on p − p0 (prop.test's Wilson bounds kept as r_wilson_*)
// — each the default; the opt-in options that reproduce R's figures are
// pinned in test_options_oracle_test.go (#644).

// detailFloats reads a []float64 detail.
func detailFloats(t *testing.T, d map[string]any, key string) []float64 {
	t.Helper()
	v, ok := d[key].([]float64)
	if !ok {
		t.Fatalf("details[%q] = %#v (%T), want []float64", key, d[key], d[key])
	}
	return v
}

// feedTwoGroups feeds a's values under split level "a" and b's under
// "b" into a two-sample fixture row test (revenue by treatment).
func feedTwoGroups(t *testing.T, schema *encoding.Schema, a, b []float64) func(RowTest) {
	return func(rt RowTest) {
		for _, side := range []struct {
			label string
			vals  []float64
		}{{"a", a}, {"b", b}} {
			for _, v := range side.vals {
				if err := rt.UpdateRow(newTestRecord(t, schema, map[string]float64{"revenue": v}, "treatment", side.label)); err != nil {
					t.Fatalf("UpdateRow: %v", err)
				}
			}
		}
	}
}

type tOracleCase struct {
	Name      string    `json:"name"`
	Variant   string    `json:"variant"`
	Values    []float64 `json:"values"`
	A         []float64 `json:"a"`
	B         []float64 `json:"b"`
	Mu        float64   `json:"mu"`
	Alpha     float64   `json:"alpha"`
	Statistic float64   `json:"statistic"`
	DF        float64   `json:"df"`
	PValue    float64   `json:"p_value"`
	CILow     float64   `json:"ci_low"`
	CIHigh    float64   `json:"ci_high"`
	Mean      any       `json:"mean"`
	Variance  any       `json:"variance"`
	Diff      float64   `json:"diff"`
}

// TestReferenceOracle_TTestWelch: TEST_T (one-sample against t.test(x,
// mu); two-sample against t.test(a, b, var.equal = FALSE)) and TEST_WELCH
// (every two-sample case) — t, df, p, the interval, the means and
// variances.
func TestReferenceOracle_TTestWelch(t *testing.T) {
	t.Parallel()

	doc := loadReference[tOracleCase](t, "test_t")
	c := newOracleCheck(t, "TEST_T / TEST_WELCH", oracleRelTol, oracleFigureAbsFloor)
	ran := 0
	for _, k := range doc.Cases {
		if k.Variant == "one_sample" {
			schema := numericFixtureSchema("revenue")
			params, _ := json.Marshal(map[string]float64{"mu": k.Mu})
			res := runRegisteredRow(t, &types.Test{Type: types.TEST_T, Field: "revenue", Alpha: k.Alpha, Params: params}, schema, func(rt RowTest) {
				for _, v := range k.Values {
					if err := rt.UpdateRow(NewRecord(schema, map[string]float64{"revenue": v})); err != nil {
						t.Fatalf("%s: UpdateRow: %v", k.Name, err)
					}
				}
			})
			if res.Variant != "one_sample" {
				t.Errorf("%s: variant %q", k.Name, res.Variant)
			}
			c.check(res.Statistic, k.Statistic, "%s statistic", k.Name)
			c.check(res.DF, k.DF, "%s df", k.Name)
			c.check(res.PValue, k.PValue, "%s p_value", k.Name)
			c.check(detailFloat(t, res.Details, "ci_low"), k.CILow, "%s ci_low", k.Name)
			c.check(detailFloat(t, res.Details, "ci_high"), k.CIHigh, "%s ci_high", k.Name)
			c.check(detailFloat(t, res.Details, "mean"), k.Mean.(float64), "%s mean", k.Name)
			c.check(detailFloat(t, res.Details, "variance"), k.Variance.(float64), "%s variance", k.Name)
			ran++
			continue
		}
		for _, typ := range []types.TestType{types.TEST_T, types.TEST_WELCH} {
			schema := twoSampleFixtureSchema()
			res := runRegisteredRow(t, &types.Test{Type: typ, Field: "revenue", SplitBy: "treatment", Alpha: k.Alpha}, schema, feedTwoGroups(t, schema, k.A, k.B))
			name := string(typ) + " " + k.Name
			if res.Variant != "welch_two_sample" || res.Type != typ {
				t.Errorf("%s: variant %q type %s", name, res.Variant, res.Type)
			}
			c.check(res.Statistic, k.Statistic, "%s statistic", name)
			c.check(res.DF, k.DF, "%s df", name)
			c.check(res.PValue, k.PValue, "%s p_value", name)
			c.check(detailFloat(t, res.Details, "ci_low"), k.CILow, "%s ci_low", name)
			c.check(detailFloat(t, res.Details, "ci_high"), k.CIHigh, "%s ci_high", name)
			c.check(detailFloat(t, res.Details, "diff"), k.Diff, "%s diff", name)
			checkPair(c, detailFloats(t, res.Details, "mean"), k.Mean, name+" mean")
			checkPair(c, detailFloats(t, res.Details, "variance"), k.Variance, name+" variance")
			ran++
		}
	}
	c.done(ran)
}

// checkPair compares a two-group detail against the golden's [a, b].
func checkPair(c *oracleCheck, got []float64, want any, what string) {
	c.t.Helper()
	w, ok := want.([]any)
	if !ok || len(w) != len(got) {
		c.t.Errorf("%s: got %v, golden %v", what, got, want)
		return
	}
	for i := range got {
		c.check(got[i], w[i].(float64), "%s[%d]", what, i)
	}
}

// TestReferenceOracle_ZTwoSample: TEST_Z_TWO_SAMPLE against the Welch
// standard error's normal test in R (2·pnorm(−|z|), qnorm interval) on
// test_t.json's two-sample inputs.
func TestReferenceOracle_ZTwoSample(t *testing.T) {
	t.Parallel()

	doc := loadReference[tOracleCase](t, "test_z_two_sample")
	c := newOracleCheck(t, "TEST_Z_TWO_SAMPLE", oracleRelTol, oracleFigureAbsFloor)
	for _, k := range doc.Cases {
		schema := twoSampleFixtureSchema()
		res := runRegisteredRow(t, &types.Test{Type: types.TEST_Z_TWO_SAMPLE, Field: "revenue", SplitBy: "treatment", Alpha: k.Alpha}, schema, feedTwoGroups(t, schema, k.A, k.B))
		c.check(res.Statistic, k.Statistic, "%s statistic", k.Name)
		c.check(res.PValue, k.PValue, "%s p_value", k.Name)
		c.check(detailFloat(t, res.Details, "ci_low"), k.CILow, "%s ci_low", k.Name)
		c.check(detailFloat(t, res.Details, "ci_high"), k.CIHigh, "%s ci_high", k.Name)
		c.check(detailFloat(t, res.Details, "diff"), k.Diff, "%s diff", k.Name)
		checkPair(c, detailFloats(t, res.Details, "mean"), k.Mean, k.Name+" mean")
		checkPair(c, detailFloats(t, res.Details, "variance"), k.Variance, k.Name+" variance")
	}
	c.done(len(doc.Cases))
}

// TestReferenceOracle_ChiSq: TEST_CHISQ independence against
// chisq.test(table, correct = FALSE) and goodness of fit against
// chisq.test(counts, p = expected) — statistic, df, p, the smallest
// expected count. The table is replayed as one record per counted unit.
func TestReferenceOracle_ChiSq(t *testing.T) {
	t.Parallel()

	doc := loadReference[struct {
		Name        string      `json:"name"`
		Variant     string      `json:"variant"`
		Table       [][]float64 `json:"table"`
		Labels      []string    `json:"labels"`
		Counts      []float64   `json:"counts"`
		Proportions []float64   `json:"expected_proportions"`
		Statistic   float64     `json:"statistic"`
		DF          float64     `json:"df"`
		PValue      float64     `json:"p_value"`
		ExpectedMin float64     `json:"expected_min"`
	}](t, "test_chisq")
	c := newOracleCheck(t, "TEST_CHISQ", oracleRelTol, oracleFigureAbsFloor)
	for _, k := range doc.Cases {
		schema := &encoding.Schema{Fields: []encoding.Field{
			{Name: "r", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
			{Name: "c", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
		}}
		spec := &types.Test{Type: types.TEST_CHISQ, Rows: "r", Cols: "c"}
		var feed func(RowTest)
		switch k.Variant {
		case "independence":
			feed = func(rt RowTest) {
				for i, row := range k.Table {
					for j, n := range row {
						for range int(n) {
							rec := NewRecord(schema, map[string]float64{
								"r": float64(dictIDOrAdd(schema, "r", "r"+strconv.Itoa(i+1))),
								"c": float64(dictIDOrAdd(schema, "c", "c"+strconv.Itoa(j+1))),
							})
							if err := rt.UpdateRow(rec); err != nil {
								t.Fatalf("%s: UpdateRow: %v", k.Name, err)
							}
						}
					}
				}
			}
		case "goodness_of_fit":
			expected := map[string]float64{}
			for i, l := range k.Labels {
				expected[l] = k.Proportions[i]
			}
			spec.Cols = ""
			spec.Params, _ = json.Marshal(map[string]any{"expected": expected})
			feed = func(rt RowTest) {
				for i, n := range k.Counts {
					for range int(n) {
						rec := NewRecord(schema, map[string]float64{"r": float64(dictIDOrAdd(schema, "r", k.Labels[i]))})
						if err := rt.UpdateRow(rec); err != nil {
							t.Fatalf("%s: UpdateRow: %v", k.Name, err)
						}
					}
				}
			}
		default:
			t.Fatalf("%s: unknown variant %q", k.Name, k.Variant)
		}
		res := runRegisteredRow(t, spec, schema, feed)
		if res.Variant != k.Variant {
			t.Errorf("%s: variant %q, want %q", k.Name, res.Variant, k.Variant)
		}
		c.check(res.Statistic, k.Statistic, "%s statistic", k.Name)
		c.check(res.DF, k.DF, "%s df", k.Name)
		c.check(res.PValue, k.PValue, "%s p_value", k.Name)
		c.check(detailFloat(t, res.Details, "expected_min"), k.ExpectedMin, "%s expected_min", k.Name)
		if low := k.ExpectedMin < 5; (len(res.Warnings) > 0) != low {
			t.Errorf("%s: expected-count warning %v, R expected_min %v", k.Name, res.Warnings, k.ExpectedMin)
		}
	}
	c.done(len(doc.Cases))
}

// TestReferenceOracle_MannWhitneyU: TEST_MANN_WHITNEY_U against
// wilcox.test(a, b, exact = FALSE, correct = TRUE) — U_a (R's W), U_b,
// U_min, the rank sum, μ_U, the tie-corrected variance, z and p.
func TestReferenceOracle_MannWhitneyU(t *testing.T) {
	t.Parallel()

	doc := loadReference[struct {
		Name   string    `json:"name"`
		A      []float64 `json:"a"`
		B      []float64 `json:"b"`
		UA     float64   `json:"u_a"`
		UB     float64   `json:"u_b"`
		UMin   float64   `json:"u_min"`
		RA     float64   `json:"r_a"`
		MuU    float64   `json:"mu_u"`
		VarU   float64   `json:"var_u"`
		Z      float64   `json:"z"`
		PValue float64   `json:"p_value"`
	}](t, "test_mann_whitney_u")
	c := newOracleCheck(t, "TEST_MANN_WHITNEY_U", oracleRelTol, oracleFigureAbsFloor)
	for _, k := range doc.Cases {
		schema := twoSampleFixtureSchema()
		res := runRegisteredRow(t, &types.Test{Type: types.TEST_MANN_WHITNEY_U, Field: "revenue", SplitBy: "treatment"}, schema, feedTwoGroups(t, schema, k.A, k.B))
		d := res.Details
		c.check(res.Statistic, k.UMin, "%s statistic (U_min)", k.Name)
		c.check(detailFloat(t, d, "u_a"), k.UA, "%s u_a", k.Name)
		c.check(detailFloat(t, d, "u_b"), k.UB, "%s u_b", k.Name)
		c.check(detailFloat(t, d, "r_a"), k.RA, "%s r_a", k.Name)
		c.check(detailFloat(t, d, "mu_u"), k.MuU, "%s mu_u", k.Name)
		c.check(detailFloat(t, d, "var_u"), k.VarU, "%s var_u", k.Name)
		c.check(detailFloat(t, d, "z"), k.Z, "%s z", k.Name)
		c.check(res.PValue, k.PValue, "%s p_value", k.Name)
	}
	c.done(len(doc.Cases))
}

// TestReferenceOracle_KruskalWallis: TEST_KRUSKAL_WALLIS against
// kruskal.test(x, g) — tie-corrected H, df, p, the per-group rank sums
// and the tie factor.
func TestReferenceOracle_KruskalWallis(t *testing.T) {
	t.Parallel()

	doc := loadReference[struct {
		Name      string               `json:"name"`
		Groups    []string             `json:"groups"`
		Values    map[string][]float64 `json:"values"`
		Statistic float64              `json:"statistic"`
		DF        float64              `json:"df"`
		PValue    float64              `json:"p_value"`
		RankSums  []float64            `json:"rank_sums"`
		TieFactor float64              `json:"tie_factor"`
	}](t, "test_kruskal_wallis")
	c := newOracleCheck(t, "TEST_KRUSKAL_WALLIS", oracleRelTol, oracleFigureAbsFloor)
	for _, k := range doc.Cases {
		schema := twoSampleFixtureSchema()
		res := runRegisteredRow(t, &types.Test{Type: types.TEST_KRUSKAL_WALLIS, Field: "revenue", SplitBy: "treatment"}, schema, func(rt RowTest) {
			for _, g := range k.Groups {
				for _, v := range k.Values[g] {
					if err := rt.UpdateRow(newTestRecord(t, schema, map[string]float64{"revenue": v}, "treatment", g)); err != nil {
						t.Fatalf("%s: UpdateRow: %v", k.Name, err)
					}
				}
			}
		})
		if got := fmt.Sprint(res.Details["groups"]); got != fmt.Sprint(k.Groups) {
			t.Errorf("%s: groups %s, want %v", k.Name, got, k.Groups)
		}
		c.check(res.Statistic, k.Statistic, "%s statistic", k.Name)
		c.check(res.DF, k.DF, "%s df", k.Name)
		c.check(res.PValue, k.PValue, "%s p_value", k.Name)
		c.check(detailFloat(t, res.Details, "tie_factor"), k.TieFactor, "%s tie_factor", k.Name)
		for i, rs := range detailFloats(t, res.Details, "rank_sums") {
			c.check(rs, k.RankSums[i], "%s rank_sums[%d]", k.Name, i)
		}
	}
	c.done(len(doc.Cases))
}

// TestReferenceOracle_PropZ: TEST_PROP_Z against prop.test(x, n, correct
// = FALSE) — z = sign(diff)·√X², p, and the interval: prop.test's Wald
// interval for two proportions, Pulse's Wald interval on p − p0 for one
// (not prop.test's Wilson interval; the generator computes Pulse's form).
func TestReferenceOracle_PropZ(t *testing.T) {
	t.Parallel()

	doc := loadReference[struct {
		Name       string          `json:"name"`
		Variant    string          `json:"variant"`
		Successes  json.RawMessage `json:"successes"`
		N          json.RawMessage `json:"n"`
		P0         float64         `json:"p0"`
		Alpha      float64         `json:"alpha"`
		Statistic  float64         `json:"statistic"`
		PValue     float64         `json:"p_value"`
		CILow      float64         `json:"ci_low"`
		CIHigh     float64         `json:"ci_high"`
		Proportion json.RawMessage `json:"proportion"`
		Pooled     float64         `json:"pooled"`
	}](t, "test_prop_z")
	c := newOracleCheck(t, "TEST_PROP_Z", oracleRelTol, oracleFigureAbsFloor)
	for _, k := range doc.Cases {
		schema := &encoding.Schema{Fields: []encoding.Field{
			{Name: "outcome", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
			{Name: "arm", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
		}}
		dictIDOrAdd(schema, "outcome", "yes")
		dictIDOrAdd(schema, "outcome", "no")
		var successes, ns []float64
		var one bool
		switch k.Variant {
		case "two_proportion_pooled":
			mustUnmarshal(t, k.Successes, &successes)
			mustUnmarshal(t, k.N, &ns)
		case "one_proportion":
			var s, n float64
			mustUnmarshal(t, k.Successes, &s)
			mustUnmarshal(t, k.N, &n)
			successes, ns, one = []float64{s}, []float64{n}, true
		default:
			t.Fatalf("%s: unknown variant %q", k.Name, k.Variant)
		}
		spec := &types.Test{Type: types.TEST_PROP_Z, Field: "outcome", Alpha: k.Alpha}
		if one {
			spec.Params, _ = json.Marshal(map[string]any{"success": "yes", "p0": k.P0})
		} else {
			spec.SplitBy = "arm"
			spec.Params, _ = json.Marshal(map[string]any{"success": "yes"})
		}
		res := runRegisteredRow(t, spec, schema, func(rt RowTest) {
			for g := range successes {
				arm := string(rune('a' + g))
				for i := range int(ns[g]) {
					outcome := "no"
					if i < int(successes[g]) {
						outcome = "yes"
					}
					vals := map[string]float64{"outcome": float64(dictIDOrAdd(schema, "outcome", outcome))}
					if !one {
						vals["arm"] = float64(dictIDOrAdd(schema, "arm", arm))
					}
					if err := rt.UpdateRow(NewRecord(schema, vals)); err != nil {
						t.Fatalf("%s: UpdateRow: %v", k.Name, err)
					}
				}
			}
		})
		if res.Variant != k.Variant {
			t.Errorf("%s: variant %q, want %q", k.Name, res.Variant, k.Variant)
		}
		c.check(res.Statistic, k.Statistic, "%s statistic", k.Name)
		c.check(res.PValue, k.PValue, "%s p_value", k.Name)
		c.check(detailFloat(t, res.Details, "ci_low"), k.CILow, "%s ci_low", k.Name)
		c.check(detailFloat(t, res.Details, "ci_high"), k.CIHigh, "%s ci_high", k.Name)
		if one {
			var ph float64
			mustUnmarshal(t, k.Proportion, &ph)
			c.check(detailFloat(t, res.Details, "proportion"), ph, "%s proportion", k.Name)
		} else {
			var props []float64
			mustUnmarshal(t, k.Proportion, &props)
			for i, p := range detailFloats(t, res.Details, "proportion") {
				c.check(p, props[i], "%s proportion[%d]", k.Name, i)
			}
			c.check(detailFloat(t, res.Details, "pooled"), k.Pooled, "%s pooled", k.Name)
		}
	}
	c.done(len(doc.Cases))
}

func mustUnmarshal(t *testing.T, raw json.RawMessage, v any) {
	t.Helper()
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
}
