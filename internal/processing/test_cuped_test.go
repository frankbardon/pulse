package processing

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// cupedRow is one fixture row: arm, outcome y, covariate x (null when
// xNull).
type cupedRow struct {
	arm   string
	y, x  float64
	xNull bool
}

func cupedSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "y", Type: encoding.FieldTypeF64},
		{Name: "x", Type: encoding.FieldTypeF64},
		{Name: "yadj", Type: encoding.FieldTypeF64},
		{Name: "arm", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
	}}
}

// cupedFixture is a deterministic two-arm sample whose outcome tracks
// its covariate (integer arithmetic only, so it is identical on every
// architecture), with every nullEvery-th covariate null (0: none).
func cupedFixture(n, nullEvery int) []cupedRow {
	rows := make([]cupedRow, 0, n)
	for i := range n {
		arm := "control"
		lift := 0.0
		if i%2 == 1 {
			arm, lift = "treatment", 1.5
		}
		x := float64((i*37)%29) + float64((i*11)%7)*0.5
		y := 0.8*x + float64((i*13)%17)*0.25 + lift
		rows = append(rows, cupedRow{arm: arm, y: y, x: x, xNull: nullEvery > 0 && i%nullEvery == 0})
	}
	return rows
}

func cupedRecords(t *testing.T, schema *encoding.Schema, rows []cupedRow, yadj []float64) []*Record {
	t.Helper()
	out := make([]*Record, len(rows))
	for i, r := range rows {
		vals := map[string]float64{"y": r.y, "x": r.x, "arm": float64(dictIDOrAdd(schema, "arm", r.arm))}
		if yadj != nil {
			vals["yadj"] = yadj[i]
		}
		out[i] = NewRecordWithNulls(schema, vals, map[string]bool{"x": r.xNull})
	}
	return out
}

// twoStepAdjusted is the two-step CUPED reference: θ = Cov(x, y)/Var(x)
// and x̄ by direct sums over the listwise rows, then y′ = y − θ(x − x̄)
// per row (NaN where the covariate is null).
func twoStepAdjusted(rows []cupedRow) (yadj []float64, theta, xbar float64) {
	var n, sx, sy float64
	for _, r := range rows {
		if !r.xNull {
			n++
			sx += r.x
			sy += r.y
		}
	}
	xbar, ybar := sx/n, sy/n
	var sxy, sxx float64
	for _, r := range rows {
		if !r.xNull {
			sxy += (r.x - xbar) * (r.y - ybar)
			sxx += (r.x - xbar) * (r.x - xbar)
		}
	}
	theta = sxy / sxx
	yadj = make([]float64, len(rows))
	for i, r := range rows {
		yadj[i] = r.y - theta*(r.x-xbar)
	}
	return yadj, theta, xbar
}

func runCupedTest(t *testing.T, spec *types.Test, schema *encoding.Schema, recs []*Record) *types.TestResult {
	t.Helper()
	factory := rowTestRegistry[spec.Type]
	rt, err := factory(spec, schema)
	if err != nil {
		t.Fatalf("%s: %v", spec.Type, err)
	}
	for _, r := range recs {
		if err := rt.UpdateRow(r); err != nil {
			t.Fatal(err)
		}
	}
	res, err := rt.Finalize()
	if err != nil {
		t.Fatalf("%s Finalize: %v", spec.Type, err)
	}
	return res
}

func nearRel(a, b, tol float64) bool {
	return math.Abs(a-b) <= tol*math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
}

// TestCuped_EqualsTwoStep: params.covariate on every mean test equals
// the two-step — θ and x̄ estimated first, then the plain test on
// y′ = y − θ(x − x̄): diff, interval, statistic, df, p, the adjusted
// per-group means / variances and Cohen's d. theta / covariate_mean are
// the direct-sum slope and mean, diff_unadjusted the raw difference,
// variance_reduction 1 − SE²′/SE².
func TestCuped_EqualsTwoStep(t *testing.T) {
	t.Parallel()

	rows := cupedFixture(400, 0)
	yadj, theta, xbar := twoStepAdjusted(rows)
	for _, typ := range []types.TestType{types.TEST_WELCH, types.TEST_T, types.TEST_Z_TWO_SAMPLE} {
		schema := cupedSchema()
		recs := cupedRecords(t, schema, rows, yadj)
		adj := runCupedTest(t, &types.Test{Type: typ, Field: "y", SplitBy: "arm", Params: json.RawMessage(`{"covariate":"x"}`)}, schema, recs)
		ref := runCupedTest(t, &types.Test{Type: typ, Field: "yadj", SplitBy: "arm"}, schema, recs)
		raw := runCupedTest(t, &types.Test{Type: typ, Field: "y", SplitBy: "arm"}, schema, recs)
		for _, c := range []struct {
			name      string
			got, want float64
		}{
			{"statistic", adj.Statistic, ref.Statistic},
			{"df", adj.DF, ref.DF},
			{"p_value", adj.PValue, ref.PValue},
			{"diff", adj.Details["diff"].(float64), ref.Details["diff"].(float64)},
			{"ci_low", adj.Details["ci_low"].(float64), ref.Details["ci_low"].(float64)},
			{"ci_high", adj.Details["ci_high"].(float64), ref.Details["ci_high"].(float64)},
			{"mean[0]", adj.Details["mean"].([]float64)[0], ref.Details["mean"].([]float64)[0]},
			{"variance[1]", adj.Details["variance"].([]float64)[1], ref.Details["variance"].([]float64)[1]},
			{"cohens_d", adj.Details["effect_size"].(map[string]any)["cohens_d"].(float64), ref.Details["effect_size"].(map[string]any)["cohens_d"].(float64)},
			{"theta", adj.Details["theta"].(float64), theta},
			{"covariate_mean", adj.Details["covariate_mean"].(float64), xbar},
			{"diff_unadjusted", adj.Details["diff_unadjusted"].(float64), raw.Details["diff"].(float64)},
		} {
			if !nearRel(c.got, c.want, 1e-9) {
				t.Errorf("%s %s = %.15g, two-step %.15g", typ, c.name, c.got, c.want)
			}
		}
		se2 := func(r *types.TestResult) float64 {
			v, n := r.Details["variance"].([]float64), r.Details["n"].([]int64)
			return v[0]/float64(n[0]) + v[1]/float64(n[1])
		}
		if vr := adj.Details["variance_reduction"].(float64); !nearRel(vr, 1-se2(ref)/se2(raw), 1e-9) || vr <= 0.5 {
			t.Errorf("%s variance_reduction %v, want 1 − %v/%v", typ, vr, se2(ref), se2(raw))
		}
		if adj.Details["n_covariate_dropped"] != int64(0) {
			t.Errorf("%s n_covariate_dropped %v", typ, adj.Details["n_covariate_dropped"])
		}
	}
}

// TestCuped_ConstantCovariate: a constant covariate adjusts nothing —
// the test is the unadjusted one — and theta / variance_reduction are
// undefined (NaN, null on the wire), never 0.
func TestCuped_ConstantCovariate(t *testing.T) {
	t.Parallel()

	rows := cupedFixture(60, 0)
	for i := range rows {
		rows[i].x = 7
	}
	schema := cupedSchema()
	recs := cupedRecords(t, schema, rows, nil)
	adj := runCupedTest(t, &types.Test{Type: types.TEST_WELCH, Field: "y", SplitBy: "arm", Params: json.RawMessage(`{"covariate":"x"}`)}, schema, recs)
	raw := runCupedTest(t, &types.Test{Type: types.TEST_WELCH, Field: "y", SplitBy: "arm"}, schema, recs)
	if adj.Statistic != raw.Statistic || adj.PValue != raw.PValue || adj.Details["diff"] != raw.Details["diff"] {
		t.Fatalf("constant covariate moved the test: %v / %v", adj.Details, raw.Details)
	}
	for _, k := range []string{"theta", "variance_reduction"} {
		if v := adj.Details[k].(float64); !math.IsNaN(v) {
			t.Errorf("%s = %v, want NaN (undefined)", k, v)
		}
	}
	if adj.Details["covariate_mean"] != 7.0 {
		t.Errorf("covariate_mean %v", adj.Details["covariate_mean"])
	}
	b, err := json.Marshal(adj)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(b, &wire); err != nil {
		t.Fatal(err)
	}
	d := wire["details"].(map[string]any)
	for _, k := range []string{"theta", "variance_reduction"} {
		if v, ok := d[k]; !ok || v != nil {
			t.Errorf("wire %s = %v (present %v), want null", k, v, ok)
		}
	}
}

// TestCuped_NullCovariateDropped: a row with a null covariate is
// dropped listwise and counted; the result equals the test over the
// complete rows alone.
func TestCuped_NullCovariateDropped(t *testing.T) {
	t.Parallel()

	rows := cupedFixture(300, 7)
	var complete []cupedRow
	dropped := 0
	for _, r := range rows {
		if r.xNull {
			dropped++
		} else {
			complete = append(complete, r)
		}
	}
	spec := func() *types.Test {
		return &types.Test{Type: types.TEST_WELCH, Field: "y", SplitBy: "arm", Params: json.RawMessage(`{"covariate":"x"}`)}
	}
	s1, s2 := cupedSchema(), cupedSchema()
	all := runCupedTest(t, spec(), s1, cupedRecords(t, s1, rows, nil))
	only := runCupedTest(t, spec(), s2, cupedRecords(t, s2, complete, nil))
	if all.Details["n_covariate_dropped"] != int64(dropped) || dropped == 0 {
		t.Fatalf("n_covariate_dropped %v, want %d", all.Details["n_covariate_dropped"], dropped)
	}
	n := all.Details["n"].([]int64)
	if n[0]+n[1] != int64(len(complete)) {
		t.Fatalf("n %v, want %d listwise rows", n, len(complete))
	}
	if all.Statistic != only.Statistic || all.Details["theta"] != only.Details["theta"] || all.Details["diff_unadjusted"] != only.Details["diff_unadjusted"] {
		t.Fatalf("listwise drop differs from the complete rows: %v / %v", all.Details, only.Details)
	}
}

// TestCuped_ComposesWithReference: naming the alphabetical-first group
// the reference negates every signed figure (adjusted and unadjusted
// difference, interval, statistic) and leaves theta and the variance
// reduction alone.
func TestCuped_ComposesWithReference(t *testing.T) {
	t.Parallel()

	rows := cupedFixture(200, 0)
	for _, typ := range []types.TestType{types.TEST_WELCH, types.TEST_T, types.TEST_Z_TWO_SAMPLE} {
		s1, s2 := cupedSchema(), cupedSchema()
		plain := runCupedTest(t, &types.Test{Type: typ, Field: "y", SplitBy: "arm", Params: json.RawMessage(`{"covariate":"x"}`)}, s1, cupedRecords(t, s1, rows, nil))
		ref := runCupedTest(t, &types.Test{Type: typ, Field: "y", SplitBy: "arm", Params: json.RawMessage(`{"covariate":"x","reference":"control"}`)}, s2, cupedRecords(t, s2, rows, nil))
		if g := ref.Details["groups"].([]string); g[0] != "treatment" || g[1] != "control" {
			t.Fatalf("%s groups %v", typ, g)
		}
		for _, k := range []string{"diff", "diff_unadjusted"} {
			if ref.Details[k].(float64) != -plain.Details[k].(float64) || ref.Details[k].(float64) <= 0 {
				t.Errorf("%s %s %v, plain %v", typ, k, ref.Details[k], plain.Details[k])
			}
		}
		if !nearRel(ref.Details["ci_low"].(float64), -plain.Details["ci_high"].(float64), 1e-12) || ref.Statistic != -plain.Statistic {
			t.Errorf("%s interval / statistic not negated", typ)
		}
		if ref.Details["theta"] != plain.Details["theta"] || !nearRel(ref.Details["variance_reduction"].(float64), plain.Details["variance_reduction"].(float64), 1e-12) {
			t.Errorf("%s theta / variance_reduction moved with the reference", typ)
		}
	}
}

// TestCuped_AbsentAddsNothing: without params.covariate the result
// carries none of the adjustment's keys (the unadjusted test).
func TestCuped_AbsentAddsNothing(t *testing.T) {
	t.Parallel()

	schema := cupedSchema()
	res := runCupedTest(t, &types.Test{Type: types.TEST_WELCH, Field: "y", SplitBy: "arm"}, schema, cupedRecords(t, schema, cupedFixture(40, 0), nil))
	for _, k := range []string{"theta", "covariate_mean", "variance_reduction", "diff_unadjusted", "n_covariate_dropped"} {
		if _, ok := res.Details[k]; ok {
			t.Errorf("unadjusted test carries %s", k)
		}
	}
}

// TestCuped_StreamingEqualsBuffered: the adjustment folds in one pass,
// so the streaming arm and the buffered arm agree byte for byte.
func TestCuped_StreamingEqualsBuffered(t *testing.T) {
	t.Parallel()

	schema := cupedSchema()
	recs := cupedRecords(t, schema, cupedFixture(500, 9), nil)
	req := func() *types.Request {
		return &types.Request{
			Tests:        []*types.Test{{Type: types.TEST_WELCH, Field: "y", SplitBy: "arm", Params: json.RawMessage(`{"covariate":"x","reference":"control"}`)}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "y"}},
		}
	}
	if !NewProcessor(schema).canStream(req()) {
		t.Fatal("a covariate-adjusted test request does not stream")
	}
	streamed, err := NewProcessor(schema).Process(context.Background(), req(), NewSliceIterator(recs))
	if err != nil {
		t.Fatal(err)
	}
	buffered, err := processBuffered(NewProcessor(schema), req(), recs)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(streamed.Tests)
	b, _ := json.Marshal(buffered.Tests)
	if string(a) != string(b) {
		t.Fatalf("streaming and buffered differ:\n%s\n%s", a, b)
	}
}

// TestCuped_FrequencyWeightEqualsExpansion: the moment fold carries the
// row weight, so under a frequency weight the adjusted test equals the
// unweighted one on the expanded rows (each row repeated w times).
func TestCuped_FrequencyWeightEqualsExpansion(t *testing.T) {
	t.Parallel()

	rows := cupedFixture(120, 0)
	ws := cupedSchema()
	ws.Fields = append(ws.Fields, encoding.Field{Name: "w", Type: encoding.FieldTypeF64})
	var weighted []*Record
	var expanded []cupedRow
	for i, r := range rows {
		w := float64(1 + i%3)
		weighted = append(weighted, NewRecord(ws, map[string]float64{"y": r.y, "x": r.x, "w": w, "arm": float64(dictIDOrAdd(ws, "arm", r.arm))}))
		for range int(w) {
			expanded = append(expanded, r)
		}
	}
	params := json.RawMessage(`{"covariate":"x"}`)
	got := runCupedTest(t, &types.Test{Type: types.TEST_WELCH, Field: "y", SplitBy: "arm", Params: params,
		Weight: types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency})}, ws, weighted)
	es := cupedSchema()
	want := runCupedTest(t, &types.Test{Type: types.TEST_WELCH, Field: "y", SplitBy: "arm", Params: params}, es, cupedRecords(t, es, expanded, nil))
	for _, k := range []string{"diff", "ci_low", "ci_high", "theta", "covariate_mean", "variance_reduction", "diff_unadjusted"} {
		if !nearRel(got.Details[k].(float64), want.Details[k].(float64), 1e-9) {
			t.Errorf("%s weighted %v, expansion %v", k, got.Details[k], want.Details[k])
		}
	}
	if !nearRel(got.Statistic, want.Statistic, 1e-9) || !nearRel(got.DF, want.DF, 1e-9) {
		t.Errorf("t %v df %v, expansion %v %v", got.Statistic, got.DF, want.Statistic, want.DF)
	}
}
