package processing

import (
	"encoding/json"
	stderrors "errors"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Goodness of fit on TEST_CHISQ (params.expected) and the one-sample
// TEST_PROP_Z (params.p0) — U48 E1-S1. Oracles: the SRM shape's χ²
// equals OVERLAY_CHISQ_VS_POP's on the same counts; hand-computed
// statistics; frequency weights equal the expanded rows; probability
// weights equal the closed form on the n_eff-scaled table.

// armRow is one record of a one-way fixture: its level and weight.
type armRow struct {
	arm string
	w   float64
}

func armSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "arm", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
		{Name: "o", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
		{Name: "w", Type: encoding.FieldTypeF64},
	}}
}

// armCounts expands {level: count} into unit-weight rows, levels in the
// given order.
func armCounts(levels []string, counts []int) []armRow {
	var out []armRow
	for i, l := range levels {
		for range counts[i] {
			out = append(out, armRow{arm: l, w: 1})
		}
	}
	return out
}

func runGOF(t *testing.T, expected string, rows []armRow, kind types.WeightKind) (*types.TestResult, error) {
	t.Helper()
	schema := armSchema()
	spec := types.Test{Type: types.TEST_CHISQ, Rows: "arm", Params: json.RawMessage(`{"expected":` + expected + `}`)}
	if kind != "" {
		spec.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: kind})
	}
	rt, err := rowTestRegistry[spec.Type](&spec, schema)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		rec := NewRecord(schema, map[string]float64{"arm": float64(dictIDOrAdd(schema, "arm", r.arm)), "w": r.w})
		if err := rt.UpdateRow(rec); err != nil {
			t.Fatal(err)
		}
	}
	return rt.Finalize()
}

func mustGOF(t *testing.T, expected string, rows []armRow, kind types.WeightKind) *types.TestResult {
	t.Helper()
	res, err := runGOF(t, expected, rows, kind)
	if err != nil {
		t.Fatalf("GOF %s: %v", expected, err)
	}
	return res
}

// TestChiSqGOF_SRMOracleEqualsChiSqVsPop: the showcase's sample-ratio
// shape (new_page 20,087 / old_page 19,913 against a planned 50/50)
// gives χ² 0.757, df 1, p 0.384 — the same statistic and p-value
// OVERLAY_CHISQ_VS_POP computes from the facet counts against a 1:1
// design population.
func TestChiSqGOF_SRMOracleEqualsChiSqVsPop(t *testing.T) {
	t.Parallel()

	rows := armCounts([]string{"old_page", "new_page"}, []int{19913, 20087})
	res := mustGOF(t, `{"new_page":0.5,"old_page":0.5}`, rows, "")
	if res.Variant != "goodness_of_fit" || res.DF != 1 {
		t.Fatalf("variant %q df %v, want goodness_of_fit / 1", res.Variant, res.DF)
	}
	if math.Abs(res.Statistic-0.757) > 5e-4 || math.Abs(res.PValue-0.384) > 5e-4 {
		t.Fatalf("χ² %v p %v, want 0.757 / 0.384", res.Statistic, res.PValue)
	}
	if want := 2 * 87.0 * 87.0 / 20000; !relEq(res.Statistic, want, 1e-12) {
		t.Fatalf("χ² %v, want %v", res.Statistic, want)
	}

	host := newFacetResultDiscrete("arm", []types.FacetValueCount{{Value: "new_page", Count: 20087}, {Value: "old_page", Count: 19913}}, 2, 40000, 40000, 0)
	pop := newFacetResultDiscrete("arm", []types.FacetValueCount{{Value: "new_page", Count: 1}, {Value: "old_page", Count: 1}}, 2, 2, 2, 0)
	view, err := ResolveFacetPopulation(pop, "arm")
	if err != nil {
		t.Fatal(err)
	}
	layer, _, err := applyChiSqVsPop(chiSqVsPopSpec(), host.Fields["arm"], view)
	if err != nil {
		t.Fatal(err)
	}
	if !relEq(res.Statistic, *layer.Summary.Statistic, 1e-12) || !relEq(res.PValue, *layer.Summary.PValue, 1e-12) ||
		res.DF != layer.Summary.Parameters["df"] {
		t.Fatalf("GOF χ² %v p %v df %v, overlay %v %v %v", res.Statistic, res.PValue, res.DF,
			*layer.Summary.Statistic, *layer.Summary.PValue, layer.Summary.Parameters["df"])
	}
	if got := res.Details["labels"].([]string); strings.Join(got, ",") != "new_page,old_page" {
		t.Fatalf("labels %v, want sorted new_page,old_page", got)
	}
	if obs := res.Details["observed"].([]int64); obs[0] != 20087 || obs[1] != 19913 {
		t.Fatalf("observed %v", obs)
	}
}

// TestChiSqGOF_AbsentLabelIsObservedZero: a label in expected that the
// data never shows still counts, as observed 0, and keeps its df.
func TestChiSqGOF_AbsentLabelIsObservedZero(t *testing.T) {
	t.Parallel()

	rows := armCounts([]string{"a", "b"}, []int{30, 70})
	res := mustGOF(t, `{"a":0.2,"b":0.5,"c":0.3}`, rows, "")
	// Expected 20 / 50 / 30: 100/20 + 400/50 + 900/30 = 5 + 8 + 30.
	if !relEq(res.Statistic, 43, 1e-12) || res.DF != 2 {
		t.Fatalf("χ² %v df %v, want 43 / 2", res.Statistic, res.DF)
	}
	if obs := res.Details["observed"].([]int64); len(obs) != 3 || obs[2] != 0 {
		t.Fatalf("observed %v, want c = 0", obs)
	}
	if exp := res.Details["expected"].([]float64); !relEq(exp[2], 30, 1e-12) {
		t.Fatalf("expected %v", exp)
	}
	if res.Details["expected_min"].(float64) != 20 {
		t.Fatalf("expected_min %v", res.Details["expected_min"])
	}
}

// TestChiSqGOF_Refusals: every params.expected refusal, and a data
// label missing from expected.
func TestChiSqGOF_Refusals(t *testing.T) {
	t.Parallel()

	rows := armCounts([]string{"a", "b"}, []int{3, 7})
	for _, c := range []struct {
		name, expected string
		reason         string
	}{
		{"sum", `{"a":0.5,"b":0.4}`, "sum_not_one"},
		{"sum_just_over", `{"a":0.5,"b":0.500000002}`, "sum_not_one"},
		{"too_few", `{"a":1}`, "too_few_labels"},
		{"empty", `{}`, "too_few_labels"},
		{"zero", `{"a":0,"b":1}`, "not_positive"},
		{"negative", `{"a":-0.5,"b":1.5}`, "not_positive"},
		{"empty_label", `{"":0.5,"b":0.5}`, "empty_label"},
	} {
		_, err := runGOF(t, c.expected, rows, "")
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_TEST_EXPECTED_INVALID || ce.Details["reason"] != c.reason {
			t.Errorf("%s: err %v, want PULSE_TEST_EXPECTED_INVALID %s", c.name, err, c.reason)
		}
	}
	// Within tolerance is accepted.
	if _, err := runGOF(t, `{"a":0.3,"b":0.7000000001}`, rows, ""); err != nil {
		t.Errorf("sum within 1e-9 refused: %v", err)
	}
	// with cols.
	schema := armSchema()
	spec := types.Test{Type: types.TEST_CHISQ, Rows: "arm", Cols: "o", Params: json.RawMessage(`{"expected":{"a":0.5,"b":0.5}}`)}
	if _, err := newChiSqRow(&spec, schema); codeOf(t, err) != errors.PULSE_TEST_EXPECTED_INVALID {
		t.Errorf("expected + cols: %v", err)
	}
	// Neither cols nor expected: the independence form's requirement.
	spec = types.Test{Type: types.TEST_CHISQ, Rows: "arm"}
	if _, err := newChiSqRow(&spec, schema); codeOf(t, err) != errors.SERVICE_VALIDATION {
		t.Errorf("rows alone: %v", err)
	}
	// A data label expected does not list.
	_, err := runGOF(t, `{"a":0.5,"c":0.5}`, rows, "")
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_TEST_EXPECTED_LABEL_MISSING {
		t.Fatalf("missing label: %v", err)
	}
	if got := ce.Details["labels"].([]string); len(got) != 1 || got[0] != "b" {
		t.Fatalf("missing labels %v, want [b]", got)
	}
	// No rows at all is a degenerate table (a grouped run's sparse code).
	if _, err := runGOF(t, `{"a":0.5,"b":0.5}`, nil, ""); codeOf(t, err) != errors.PULSE_TEST_CONTINGENCY_DEGENERATE {
		t.Fatalf("empty: %v", err)
	}
}

// gofWeightedFixture: three arms, uneven integer weights.
func gofWeightedFixture() []armRow {
	arms := strings.Split("a a b c a b b c a c a b", " ")
	ws := []float64{3, 1, 2, 5, 1, 4, 2, 1, 3, 2, 6, 1}
	out := make([]armRow, len(arms))
	for i := range arms {
		out[i] = armRow{arm: arms[i], w: ws[i]}
	}
	return out
}

const gofWeightedExpected = `{"a":0.5,"b":0.3,"c":0.2}`

// TestChiSqGOF_Weighted: unity weights reproduce the unweighted result
// byte for byte; frequency weights equal the expanded rows; probability
// weights equal Pearson on the table scaled to Kish n_eff.
func TestChiSqGOF_Weighted(t *testing.T) {
	t.Parallel()

	rows := gofWeightedFixture()
	ones := append([]armRow(nil), rows...)
	for i := range ones {
		ones[i].w = 1
	}
	base, _ := json.Marshal(mustGOF(t, gofWeightedExpected, ones, ""))
	for _, kind := range []types.WeightKind{types.WeightKindFrequency, types.WeightKindProbability} {
		got := mustGOF(t, gofWeightedExpected, ones, kind)
		if b := shedWeightKeys(got); string(b) != string(base) {
			t.Fatalf("%s unity differs:\n got %s\nwant %s", kind, b, base)
		}
	}

	var expanded []armRow
	for _, r := range rows {
		for range int(r.w) {
			expanded = append(expanded, armRow{arm: r.arm, w: 1})
		}
	}
	freq := mustGOF(t, gofWeightedExpected, rows, types.WeightKindFrequency)
	want := mustGOF(t, gofWeightedExpected, expanded, "")
	if !relEq(freq.Statistic, want.Statistic, 1e-12) || freq.DF != want.DF || !relEq(freq.PValue, want.PValue, 1e-12) {
		t.Fatalf("frequency %v/%v/%v, expanded %v/%v/%v", freq.Statistic, freq.DF, freq.PValue, want.Statistic, want.DF, want.PValue)
	}
	if freq.Details["n"].(int64) != int64(len(rows)) || freq.Details["sum_weights"].(float64) != float64(len(expanded)) {
		t.Fatalf("frequency n %v sum_weights %v", freq.Details["n"], freq.Details["sum_weights"])
	}
	if obs := freq.Details["observed"].([]float64); sum(obs) != float64(len(expanded)) {
		t.Fatalf("frequency observed %v not Σw", obs)
	}

	// Probability: T*_i = n_eff·Σw_i/Σw against n_eff·p_i.
	props := map[string]float64{"a": 0.5, "b": 0.3, "c": 0.2}
	sw, sw2 := 0.0, 0.0
	cell := map[string]float64{}
	for _, r := range rows {
		cell[r.arm] += r.w
		sw += r.w
		sw2 += r.w * r.w
	}
	nEff := sw * sw / sw2
	var x2 float64
	for l, p := range props {
		obs, exp := nEff*cell[l]/sw, nEff*p
		x2 += (obs - exp) * (obs - exp) / exp
	}
	prob := mustGOF(t, gofWeightedExpected, rows, types.WeightKindProbability)
	if !relEq(prob.Statistic, x2, 1e-10) || prob.DF != 2 {
		t.Fatalf("probability χ² %v df %v, want %v / 2", prob.Statistic, prob.DF, x2)
	}
	if !relEq(prob.Details["n_eff"].(float64), nEff, 1e-12) {
		t.Fatalf("n_eff %v, want %v", prob.Details["n_eff"], nEff)
	}
	if !relEq(prob.Details["expected_min"].(float64), nEff*0.2, 1e-12) {
		t.Fatalf("expected_min %v, want n_eff·0.2 = %v", prob.Details["expected_min"], nEff*0.2)
	}
}

// --- TEST_PROP_Z one-proportion (params.p0) ---

func runPropOne(t *testing.T, params string, rows []armRow, kind types.WeightKind) (*types.TestResult, error) {
	t.Helper()
	schema := armSchema()
	// The success level is a dictionary entry before the test is built,
	// as in a loaded cohort.
	var p struct {
		Success string `json:"success"`
	}
	if err := json.Unmarshal([]byte(params), &p); err != nil {
		t.Fatal(err)
	}
	dictIDOrAdd(schema, "arm", p.Success)
	spec := types.Test{Type: types.TEST_PROP_Z, Field: "arm", Params: json.RawMessage(params)}
	if kind != "" {
		spec.Weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: kind})
	}
	rt, err := rowTestRegistry[spec.Type](&spec, schema)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		rec := NewRecord(schema, map[string]float64{"arm": float64(dictIDOrAdd(schema, "arm", r.arm)), "w": r.w})
		if err := rt.UpdateRow(rec); err != nil {
			t.Fatal(err)
		}
	}
	return rt.Finalize()
}

// TestPropZOne_HandComputed: 120 successes in 200 against p0 = 0.5 —
// the score z (null SE), the Wald interval on p̂ − p0 and Cohen's h.
func TestPropZOne_HandComputed(t *testing.T) {
	t.Parallel()

	rows := armCounts([]string{"yes", "no"}, []int{120, 80})
	res, err := runPropOne(t, `{"success":"yes","p0":0.5}`, rows, "")
	if err != nil {
		t.Fatal(err)
	}
	z := 0.1 / math.Sqrt(0.25/200)
	if res.Variant != "one_proportion" || !relEq(res.Statistic, z, 1e-12) {
		t.Fatalf("variant %q z %v, want one_proportion %v", res.Variant, res.Statistic, z)
	}
	if !relEq(res.PValue, math.Erfc(z/math.Sqrt2), 1e-9) {
		t.Fatalf("p %v, want %v", res.PValue, math.Erfc(z/math.Sqrt2))
	}
	half := normalCriticalTwoSided(0.05) * math.Sqrt(0.6*0.4/200)
	d := res.Details
	if !relEq(d["diff"].(float64), 0.1, 1e-12) || !relEq(d["ci_low"].(float64), 0.1-half, 1e-12) || !relEq(d["ci_high"].(float64), 0.1+half, 1e-12) {
		t.Fatalf("diff %v ci %v..%v", d["diff"], d["ci_low"], d["ci_high"])
	}
	if d["n"].(int64) != 200 || d["successes"].(int64) != 120 || d["proportion"].(float64) != 0.6 || d["p0"].(float64) != 0.5 {
		t.Fatalf("details %v", d)
	}
	h := 2*math.Asin(math.Sqrt(0.6)) - 2*math.Asin(math.Sqrt(0.5))
	if got := d["effect_size"].(map[string]any)["cohens_h"].(float64); !relEq(got, h, 1e-12) {
		t.Fatalf("cohens_h %v, want %v", got, h)
	}
}

// TestPropZOne_Weighted: unity weights reproduce the unweighted result;
// frequency weights equal the expanded rows; probability weights read
// p̂ = Σw_success/Σw on N* = Kish n_eff.
func TestPropZOne_Weighted(t *testing.T) {
	t.Parallel()

	rows := gofWeightedFixture() // success = "a"
	const params = `{"success":"a","p0":0.4}`
	ones := append([]armRow(nil), rows...)
	for i := range ones {
		ones[i].w = 1
	}
	base, err := runPropOne(t, params, ones, "")
	if err != nil {
		t.Fatal(err)
	}
	baseJSON, _ := json.Marshal(base)
	for _, kind := range []types.WeightKind{types.WeightKindFrequency, types.WeightKindProbability} {
		got, err := runPropOne(t, params, ones, kind)
		if err != nil {
			t.Fatal(err)
		}
		if b := shedWeightKeys(got); string(b) != string(baseJSON) {
			t.Fatalf("%s unity differs:\n got %s\nwant %s", kind, b, baseJSON)
		}
	}
	var expanded []armRow
	for _, r := range rows {
		for range int(r.w) {
			expanded = append(expanded, armRow{arm: r.arm, w: 1})
		}
	}
	freq, _ := runPropOne(t, params, rows, types.WeightKindFrequency)
	want, _ := runPropOne(t, params, expanded, "")
	for _, k := range []string{"diff", "ci_low", "ci_high", "proportion"} {
		if !relEq(freq.Details[k].(float64), want.Details[k].(float64), 1e-12) {
			t.Fatalf("frequency %s %v, expanded %v", k, freq.Details[k], want.Details[k])
		}
	}
	if !relEq(freq.Statistic, want.Statistic, 1e-12) || !relEq(freq.PValue, want.PValue, 1e-12) {
		t.Fatalf("frequency z %v p %v, expanded %v %v", freq.Statistic, freq.PValue, want.Statistic, want.PValue)
	}
	if freq.Details["successes"].(float64) != float64(want.Details["successes"].(int64)) {
		t.Fatalf("frequency successes %v, want Σw_success %v", freq.Details["successes"], want.Details["successes"])
	}

	var sw, sw2, yes float64
	for _, r := range rows {
		sw += r.w
		sw2 += r.w * r.w
		if r.arm == "a" {
			yes += r.w
		}
	}
	nEff, ph := sw*sw/sw2, yes/sw
	z := (ph - 0.4) / math.Sqrt(0.4*0.6/nEff)
	prob, _ := runPropOne(t, params, rows, types.WeightKindProbability)
	if !relEq(prob.Statistic, z, 1e-12) || !relEq(prob.Details["proportion"].(float64), ph, 1e-15) {
		t.Fatalf("probability z %v p̂ %v, want %v %v", prob.Statistic, prob.Details["proportion"], z, ph)
	}
	half := normalCriticalTwoSided(0.05) * math.Sqrt(ph*(1-ph)/nEff)
	if !relEq(prob.Details["ci_high"].(float64), ph-0.4+half, 1e-12) {
		t.Fatalf("probability ci_high %v, want %v", prob.Details["ci_high"], ph-0.4+half)
	}
	if !relEq(prob.Details["n_eff"].(float64), nEff, 1e-12) || prob.Details["n"].(int64) != int64(len(rows)) {
		t.Fatalf("probability n_eff %v n %v", prob.Details["n_eff"], prob.Details["n"])
	}
}

// TestPropZOne_Refusals: p0 outside (0, 1); p0 beside split_by; no
// split_by and no p0 keeps the two-proportion requirement; no rows.
func TestPropZOne_Refusals(t *testing.T) {
	t.Parallel()

	rows := armCounts([]string{"yes", "no"}, []int{3, 7})
	for _, p := range []string{`0`, `1`, `-0.2`, `1.5`} {
		_, err := runPropOne(t, `{"success":"yes","p0":`+p+`}`, rows, "")
		var ce *errors.CodedError
		if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_TEST_P0_INVALID || ce.Details["reason"] != "out_of_range" {
			t.Errorf("p0 %s: %v", p, err)
		}
	}
	schema := armSchema()
	spec := types.Test{Type: types.TEST_PROP_Z, Field: "arm", SplitBy: "o", Params: json.RawMessage(`{"success":"yes","p0":0.5}`)}
	if _, err := newPropZRow(&spec, schema); codeOf(t, err) != errors.PULSE_TEST_P0_INVALID {
		t.Errorf("p0 + split_by: %v", err)
	}
	spec = types.Test{Type: types.TEST_PROP_Z, Field: "arm", Params: json.RawMessage(`{"success":"yes"}`)}
	if _, err := newPropZRow(&spec, schema); codeOf(t, err) != errors.PROCESSING_CONFIG {
		t.Errorf("no split_by, no p0: %v", err)
	}
	if _, err := runPropOne(t, `{"success":"yes","p0":0.5}`, nil, ""); codeOf(t, err) != errors.PULSE_TEST_INSUFFICIENT_N {
		t.Errorf("no rows: %v", err)
	}
}
