package processing

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// CUPED (params.covariate) on TEST_PROP_Z, under both weight kinds, and
// against an independent two-step: θ and x̄ by direct weighted sums over
// the listwise rows, then the test on y′ = y − θ(x − x̄) — Pulse's own
// weighted test for the mean tests, a hand Wald interval for the
// proportion (whose y is the success indicator).

// cwRow is one weighted fixture row: arm, outcome y (also the success
// indicator conv: y > convCut), covariate x, weight w.
type cwRow struct {
	arm     string
	y, x, w float64
	xNull   bool
}

// convCut splits the fixture's outcome into the success indicator.
const convCut = 12.0

func (r cwRow) conv() float64 {
	if r.y > convCut {
		return 1
	}
	return 0
}

func cwSchema() *encoding.Schema {
	arm, conv := encoding.NewDictionary(), encoding.NewDictionary()
	for _, l := range []string{"control", "treatment"} {
		if _, err := arm.Add(l); err != nil {
			panic(err)
		}
	}
	for _, l := range []string{"no", "yes"} {
		if _, err := conv.Add(l); err != nil {
			panic(err)
		}
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "y", Type: encoding.FieldTypeF64},
		{Name: "x", Type: encoding.FieldTypeF64},
		{Name: "yadj", Type: encoding.FieldTypeF64},
		{Name: "w", Type: encoding.FieldTypeF64},
		{Name: "arm", Type: encoding.FieldTypeCategoricalU8, Dictionary: arm},
		{Name: "conv", Type: encoding.FieldTypeCategoricalU8, Dictionary: conv},
	}}
}

// cwFixture is cupedFixture with a weight: frequency weights are the
// integers 1..3; probability weights are fractional and heavier in the
// treatment arm, so each arm's Kish scale c_g differs.
func cwFixture(n int, kind types.WeightKind, nullEvery int) []cwRow {
	base := cupedFixture(n, nullEvery)
	out := make([]cwRow, len(base))
	for i, r := range base {
		w := float64(1 + i%3)
		if kind == types.WeightKindProbability {
			w = 0.25 + float64((i*5)%7)*0.5
			if r.arm == "treatment" {
				w += 0.75
			}
		}
		out[i] = cwRow{arm: r.arm, y: r.y, x: r.x, w: w, xNull: r.xNull}
	}
	return out
}

func cwRecords(schema *encoding.Schema, rows []cwRow, yadj []float64) []*Record {
	out := make([]*Record, len(rows))
	for i, r := range rows {
		convLevel := "no"
		if r.conv() == 1 {
			convLevel = "yes"
		}
		vals := map[string]float64{"y": r.y, "x": r.x, "w": r.w,
			"arm": float64(dictIDOrAdd(schema, "arm", r.arm)), "conv": float64(dictIDOrAdd(schema, "conv", convLevel))}
		if yadj != nil {
			vals["yadj"] = yadj[i]
		}
		out[i] = NewRecordWithNulls(schema, vals, map[string]bool{"x": r.xNull})
	}
	return out
}

// weightedTwoStep is the weighted two-step's first step: θ =
// Σw(x − x̄)(y − ȳ)/Σw(x − x̄)² and x̄ = Σwx/Σw over the listwise rows
// with the RAW weights (scale-free, so the kind does not move them),
// then y′ = y − θ(x − x̄) per row. yOf picks the outcome (y, or the
// success indicator).
func weightedTwoStep(rows []cwRow, yOf func(cwRow) float64) (yadj []float64, theta, xbar float64) {
	var sw, sx, sy float64
	for _, r := range rows {
		if !r.xNull {
			sw += r.w
			sx += r.w * r.x
			sy += r.w * yOf(r)
		}
	}
	xbar, ybar := sx/sw, sy/sw
	var sxy, sxx float64
	for _, r := range rows {
		if !r.xNull {
			sxy += r.w * (r.x - xbar) * (yOf(r) - ybar)
			sxx += r.w * (r.x - xbar) * (r.x - xbar)
		}
	}
	theta = sxy / sxx
	yadj = make([]float64, len(rows))
	for i, r := range rows {
		yadj[i] = yOf(r) - theta*(r.x-xbar)
	}
	return yadj, theta, xbar
}

// waldArm is one arm of the hand Wald reference: the weighted mean of v,
// its weighted population variance Σw(v − m)²/Σw and N* (Σw under
// frequency, Kish (Σw)²/Σw² under probability).
func waldArm(rows []cwRow, v []float64, arm string, kind types.WeightKind) (mean, variance, nStar float64) {
	var sw, sww, sv float64
	for i, r := range rows {
		if r.arm == arm && !r.xNull {
			sw += r.w
			sww += r.w * r.w
			sv += r.w * v[i]
		}
	}
	mean = sv / sw
	var m2 float64
	for i, r := range rows {
		if r.arm == arm && !r.xNull {
			m2 += r.w * (v[i] - mean) * (v[i] - mean)
		}
	}
	nStar = sw
	if kind == types.WeightKindProbability {
		nStar = sw * sw / sww
	}
	return mean, m2 / sw, nStar
}

func cwWeight(kind types.WeightKind) types.SlotWeight {
	if kind == "" {
		return types.SlotWeight{}
	}
	return types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: kind})
}

// TestCupedPropZ_EqualsTwoStepOnIndicator: TEST_PROP_Z + covariate is
// the two-step on the success indicator I — θ = Cov_w(x, I)/Var_w(x),
// I′ = I − θ(x − x̄) — read by the Wald form: diff = Ī′_a − Ī′_b, SE² =
// Σ Var_w(I′_g)/N*_g (the population variance, so θ = 0 is the
// unadjusted p̂(1 − p̂)/N* exactly), z = diff/SE, the interval
// diff ± z_{α/2}·SE. Unweighted and under both weight kinds.
func TestCupedPropZ_EqualsTwoStepOnIndicator(t *testing.T) {
	for _, kind := range []types.WeightKind{"", types.WeightKindFrequency, types.WeightKindProbability} {
		rows := cwFixture(600, kind, 11)
		if kind == "" {
			for i := range rows {
				rows[i].w = 1
			}
		}
		indicator := func(r cwRow) float64 { return r.conv() }
		ind := make([]float64, len(rows))
		for i, r := range rows {
			ind[i] = r.conv()
		}
		yadj, theta, xbar := weightedTwoStep(rows, indicator)
		ma, va, na := waldArm(rows, yadj, "control", kind)
		mb, vb, nb := waldArm(rows, yadj, "treatment", kind)
		ra, rva, _ := waldArm(rows, ind, "control", kind)
		rb, rvb, _ := waldArm(rows, ind, "treatment", kind)
		diff, se := ma-mb, math.Sqrt(va/na+vb/nb)
		seRaw2 := rva/na + rvb/nb
		zcrit := normalCriticalTwoSided(0.05)

		schema := cwSchema()
		res := runCupedTest(t, &types.Test{Type: types.TEST_PROP_Z, Field: "conv", SplitBy: "arm", Weight: cwWeight(kind),
			Params: json.RawMessage(`{"success":"yes","covariate":"x"}`)}, schema, cwRecords(schema, rows, nil))
		d := res.Details
		if res.Variant != "two_proportion_adjusted" {
			t.Errorf("%q variant %s", kind, res.Variant)
		}
		z := diff / se
		for _, c := range []struct {
			name      string
			got, want float64
		}{
			{"diff", d["diff"].(float64), diff},
			{"ci_low", d["ci_low"].(float64), diff - zcrit*se},
			{"ci_high", d["ci_high"].(float64), diff + zcrit*se},
			{"statistic", res.Statistic, z},
			{"p_value", res.PValue, math.Erfc(math.Abs(z) / math.Sqrt2)},
			{"proportion[0]", d["proportion"].([]float64)[0], ma},
			{"proportion[1]", d["proportion"].([]float64)[1], mb},
			{"theta", d["theta"].(float64), theta},
			{"covariate_mean", d["covariate_mean"].(float64), xbar},
			{"diff_unadjusted", d["diff_unadjusted"].(float64), ra - rb},
			{"variance_reduction", d["variance_reduction"].(float64), 1 - (se*se)/seRaw2},
			{"cohens_h", d["effect_size"].(map[string]any)["cohens_h"].(float64), cohensH(ma, mb)},
		} {
			if !nearRel(c.got, c.want, 1e-9) {
				t.Errorf("%q %s = %.15g, two-step %.15g", kind, c.name, c.got, c.want)
			}
		}
		if vr := d["variance_reduction"].(float64); !(vr > 0.05) {
			t.Errorf("%q variance_reduction %v: the fixture's covariate should explain the indicator", kind, vr)
		}
		if d["n_covariate_dropped"].(int64) == 0 {
			t.Errorf("%q no covariate dropped", kind)
		}
		if kind != "" {
			if sw := d["sum_weights"].([]float64); len(sw) != 2 || sw[0] <= 0 {
				t.Errorf("%q sum_weights %v", kind, d["sum_weights"])
			}
		}
	}
}

// TestCupedPropZ_ConstantCovariate: a constant covariate adjusts
// nothing — the interval is the unadjusted Wald interval and the
// statistic its Wald z — and theta / variance_reduction are undefined.
func TestCupedPropZ_ConstantCovariate(t *testing.T) {
	rows := cwFixture(200, types.WeightKindProbability, 0)
	for i := range rows {
		rows[i].x = 4
	}
	s1, s2 := cwSchema(), cwSchema()
	spec := func(params string) *types.Test {
		return &types.Test{Type: types.TEST_PROP_Z, Field: "conv", SplitBy: "arm", Weight: cwWeight(types.WeightKindProbability), Params: json.RawMessage(params)}
	}
	adj := runCupedTest(t, spec(`{"success":"yes","covariate":"x"}`), s1, cwRecords(s1, rows, nil))
	raw := runCupedTest(t, spec(`{"success":"yes"}`), s2, cwRecords(s2, rows, nil))
	for _, k := range []string{"diff", "ci_low", "ci_high", "pooled"} {
		if !nearRel(adj.Details[k].(float64), raw.Details[k].(float64), 1e-12) {
			t.Errorf("%s adjusted %v, unadjusted %v", k, adj.Details[k], raw.Details[k])
		}
	}
	for _, k := range []string{"theta", "variance_reduction"} {
		if v := adj.Details[k].(float64); !math.IsNaN(v) {
			t.Errorf("%s = %v, want NaN", k, v)
		}
	}
}

// TestCupedPropZ_Refusals: one-proportion (p0, no split_by) refuses a
// covariate (the adjustment compares two groups); a categorical
// covariate and the outcome itself are refused like on the mean tests.
func TestCupedPropZ_Refusals(t *testing.T) {
	for _, tc := range []struct {
		name, split, params, reason string
	}{
		{"one-proportion", "", `{"success":"yes","p0":0.3,"covariate":"x"}`, "without_split_by"},
		{"categorical covariate", "arm", `{"success":"yes","covariate":"arm"}`, "not_numeric"},
		{"the outcome", "arm", `{"success":"yes","covariate":"conv"}`, "is_field"},
	} {
		_, err := rowTestRegistry[types.TEST_PROP_Z](&types.Test{Type: types.TEST_PROP_Z, Field: "conv", SplitBy: tc.split, Params: json.RawMessage(tc.params)}, cwSchema())
		var ce *errors.CodedError
		if !asCoded(err, &ce) || ce.Code != errors.PULSE_TEST_COVARIATE_INVALID || ce.Details["reason"] != tc.reason {
			t.Errorf("%s: %v, want PULSE_TEST_COVARIATE_INVALID %s", tc.name, err, tc.reason)
		}
	}
}

// TestCuped_WeightedEqualsWeightedTwoStep: on every mean test under
// both weight kinds, the covariate-adjusted test equals the weighted
// two-step — the weighted θ and x̄ (raw weights), then the same
// weighted test on y′ — statistic, df, p, diff, interval, adjusted
// means / variances, Cohen's d and the per-arm sum_weights / n_eff;
// the probability weights differ between arms, so each arm reads its
// own Kish scale.
func TestCuped_WeightedEqualsWeightedTwoStep(t *testing.T) {
	for _, kind := range []types.WeightKind{types.WeightKindFrequency, types.WeightKindProbability} {
		rows := cwFixture(500, kind, 13)
		yadj, theta, xbar := weightedTwoStep(rows, func(r cwRow) float64 { return r.y })
		for _, typ := range []types.TestType{types.TEST_WELCH, types.TEST_T, types.TEST_Z_TWO_SAMPLE} {
			schema := cwSchema()
			// The two-step's second step reads the listwise rows only.
			var listwise []cwRow
			var listAdj []float64
			for i, r := range rows {
				if !r.xNull {
					listwise = append(listwise, r)
					listAdj = append(listAdj, yadj[i])
				}
			}
			adj := runCupedTest(t, &types.Test{Type: typ, Field: "y", SplitBy: "arm", Weight: cwWeight(kind), Params: json.RawMessage(`{"covariate":"x"}`)}, schema, cwRecords(schema, rows, nil))
			s2 := cwSchema()
			ref := runCupedTest(t, &types.Test{Type: typ, Field: "yadj", SplitBy: "arm", Weight: cwWeight(kind)}, s2, cwRecords(s2, listwise, listAdj))
			f := func(r *types.TestResult, k string, i int) float64 {
				switch v := r.Details[k].(type) {
				case []float64:
					return v[i]
				case map[string]any:
					return v["cohens_d"].(float64)
				}
				return r.Details[k].(float64)
			}
			for _, c := range []struct {
				name      string
				got, want float64
			}{
				{"statistic", adj.Statistic, ref.Statistic},
				{"df", adj.DF, ref.DF},
				{"p_value", adj.PValue, ref.PValue},
				{"diff", f(adj, "diff", 0), f(ref, "diff", 0)},
				{"ci_low", f(adj, "ci_low", 0), f(ref, "ci_low", 0)},
				{"ci_high", f(adj, "ci_high", 0), f(ref, "ci_high", 0)},
				{"mean[1]", f(adj, "mean", 1), f(ref, "mean", 1)},
				{"variance[0]", f(adj, "variance", 0), f(ref, "variance", 0)},
				{"cohens_d", f(adj, "effect_size", 0), f(ref, "effect_size", 0)},
				{"sum_weights[0]", f(adj, "sum_weights", 0), f(ref, "sum_weights", 0)},
				{"sum_weights[1]", f(adj, "sum_weights", 1), f(ref, "sum_weights", 1)},
				{"theta", f(adj, "theta", 0), theta},
				{"covariate_mean", f(adj, "covariate_mean", 0), xbar},
			} {
				if !nearRel(c.got, c.want, 1e-9) {
					t.Errorf("%s %s %s = %.15g, weighted two-step %.15g", kind, typ, c.name, c.got, c.want)
				}
			}
			if kind == types.WeightKindProbability {
				for i := range 2 {
					if !nearRel(f(adj, "n_eff", i), f(ref, "n_eff", i), 1e-12) {
						t.Errorf("%s n_eff[%d] %v, two-step %v", typ, i, adj.Details["n_eff"], ref.Details["n_eff"])
					}
				}
			} else if _, ok := adj.Details["n_eff"]; ok {
				t.Errorf("%s frequency result carries n_eff", typ)
			}
			if n := adj.Details["n"].([]int64); n[0]+n[1] != int64(len(listwise)) {
				t.Errorf("%s %s n %v, want %d listwise rows", kind, typ, n, len(listwise))
			}
		}
	}
}

// TestCuped_ProbabilityScaleInvariant: probability weights are defined
// up to scale — weights × 0.37 answer the same adjusted test (θ and x̄
// read the raw weights, which a common scale leaves alone).
func TestCuped_ProbabilityScaleInvariant(t *testing.T) {
	rows := cwFixture(300, types.WeightKindProbability, 0)
	scaled := append([]cwRow(nil), rows...)
	for i := range scaled {
		scaled[i].w *= 0.37
	}
	for _, typ := range []types.TestType{types.TEST_WELCH, types.TEST_PROP_Z} {
		params := `{"covariate":"x"}`
		field := "y"
		if typ == types.TEST_PROP_Z {
			params, field = `{"success":"yes","covariate":"x"}`, "conv"
		}
		s1, s2 := cwSchema(), cwSchema()
		spec := func() *types.Test {
			return &types.Test{Type: typ, Field: field, SplitBy: "arm", Weight: cwWeight(types.WeightKindProbability), Params: json.RawMessage(params)}
		}
		a := runCupedTest(t, spec(), s1, cwRecords(s1, rows, nil))
		b := runCupedTest(t, spec(), s2, cwRecords(s2, scaled, nil))
		for _, k := range []string{"diff", "ci_low", "ci_high", "theta", "covariate_mean", "variance_reduction"} {
			if !nearRel(a.Details[k].(float64), b.Details[k].(float64), 1e-9) {
				t.Errorf("%s %s %v, scaled %v", typ, k, a.Details[k], b.Details[k])
			}
		}
		if !nearRel(a.PValue, b.PValue, 1e-9) {
			t.Errorf("%s p %v, scaled %v", typ, a.PValue, b.PValue)
		}
	}
}
