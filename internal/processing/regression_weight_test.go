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

// Weighted regressions through the orchestrator (U12 E4-S1): the
// resolved weight is stamped onto weight-aware REG_* slots, invalid
// weights are tallied once per field (PULSE_WEIGHT_INVALID_ROWS), the
// streaming and buffered paths answer identically, and a probability
// fit whose n_eff falls below p + 1 warns (PULSE_WEIGHT_LOW_NEFF).

func regWeightSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "x1", Type: encoding.FieldTypeF64},
		{Name: "x2", Type: encoding.FieldTypeF64},
		{Name: "y", Type: encoding.FieldTypeF64},
		{Name: "w", Type: encoding.FieldTypeF64},
		{Name: "g", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
	}}
}

func regWeightRecords(schema *encoding.Schema, ws []float64) []*Record {
	x1 := []float64{1.2, 2.5, 3.1, 4.8, 5.0, 6.3, 7.7, 8.1, 9.4, 10.2}
	x2 := []float64{0.4, -1.3, 2.2, 0.9, -0.5, 1.7, 3.3, -2.1, 0.0, 1.1}
	noise := []float64{0.9, -0.4, 1.3, 0.2, -0.8, 0.5, -1.1, 0.7, -0.3, 1.0}
	var out []*Record
	for i := range ws {
		g := []string{"a", "b"}[i%2]
		out = append(out, NewRecord(schema, map[string]float64{
			"x1": x1[i], "x2": x2[i], "y": 1.5 + 0.8*x1[i] - 1.2*x2[i] + noise[i], "w": ws[i],
			"g": float64(dictIDOrAdd(schema, "g", g)),
		}))
	}
	return out
}

func TestWeightedRegressions_ProcessorStampsAndTallies(t *testing.T) {
	schema := regWeightSchema()
	recs := regWeightRecords(schema, []float64{1, 3, 2, 1, 4, 2, 1, 5, 1, 2})
	recs = append(recs, NewRecord(schema, map[string]float64{"x1": 50, "x2": 1, "y": 1, "w": -1, "g": 0}))
	w := &types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency}
	req := func(grouped bool) *types.Request {
		r := &types.Request{Weight: w,
			Regressions: []*types.RegressionSpec{
				{Type: types.REG_OLS, Name: "ols", Target: "y", Predictors: []string{"x1", "x2"}},
				{Type: types.REG_OLS, Name: "ridge", Target: "y", Predictors: []string{"x1", "x2"}, Penalty: "l2", Alpha: 0.2},
				{Type: types.REG_BAYES_LINEAR, Name: "bayes", Target: "y", Predictors: []string{"x1", "x2"}},
			},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x1", Weight: types.NullSlotWeight()}}}
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
		for _, r := range resp.Regressions {
			if r.SumWeights != 22 || r.NObs != 10 || r.NEff != 0 {
				t.Fatalf("grouped=%v %s: sum_weights %v n_obs %d n_eff %v", grouped, r.Name, r.SumWeights, r.NObs, r.NEff)
			}
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
		results[i], _ = json.Marshal(resp.Regressions)
	}
	if string(results[0]) != string(results[1]) {
		t.Fatalf("streaming and buffered differ:\n%s\n%s", results[0], results[1])
	}
	// Strict: the invalid row is an error.
	p := NewProcessor(schema)
	p.SetWeighting(nil, true)
	if _, err := p.Process(context.Background(), req(false), NewSliceIterator(recs)); err == nil {
		t.Fatal("strict: invalid weight row did not error")
	} else if ce, ok := err.(*errors.CodedError); !ok || ce.Code != errors.PULSE_WEIGHT_INVALID_ROWS {
		t.Fatalf("strict code: %v", err)
	}

	stamped := StampWeights(&types.Request{Weight: w, Regressions: []*types.RegressionSpec{
		{Type: types.REG_OLS}, {Type: types.REG_BAYES_LINEAR}, {Type: types.REG_GLM},
		{Type: types.REG_OLS, Resample: "bootstrap"}, {Type: types.REG_OLS, Weight: types.NullSlotWeight()},
	}}, nil)
	got := make([]bool, len(stamped.Regressions))
	for i, r := range stamped.Regressions {
		got[i] = r.Weight.Spec() != nil
	}
	if want := []bool{true, true, true, false, false}; !equalBools(got, want) {
		t.Fatalf("stamped = %v, want %v", got, want)
	}
	// A regression slot weight alone names a weight (no request
	// weight): stamping runs and spells the kind out.
	own := StampWeights(&types.Request{Regressions: []*types.RegressionSpec{{Type: types.REG_OLS, Weight: types.SlotWeightField("w")}}}, nil)
	if sp := own.Regressions[0].Weight.Spec(); sp == nil || sp.Kind != types.WeightKindProbability || NewWeightRowTally(own) == nil {
		t.Fatalf("slot-only regression weight not stamped / tallied: %+v", sp)
	}
}

func equalBools(a, b []bool) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestWeightedRegressions_LowNEff: very uneven probability weights leave
// n_eff below p + 1 — one PULSE_WEIGHT_LOW_NEFF warning naming the
// regression, an error under strict, on both fit paths; frequency
// weights never warn.
func TestWeightedRegressions_LowNEff(t *testing.T) {
	schema := regWeightSchema()
	recs := regWeightRecords(schema, []float64{1000, 1, 1, 1, 1, 1})
	for _, grouped := range []bool{false, true} {
		run := func(kind types.WeightKind, strict bool) (*types.Response, error) {
			p := NewProcessor(schema)
			p.SetWeighting(nil, strict)
			req := &types.Request{
				Weight:       &types.WeightSpec{Field: "w", Kind: kind},
				Regressions:  []*types.RegressionSpec{{Type: types.REG_OLS, Name: "fit", Target: "y", Predictors: []string{"x1", "x2"}}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x1", Weight: types.NullSlotWeight()}},
			}
			if grouped {
				req.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}}
			}
			return p.Process(context.Background(), req, NewSliceIterator(recs))
		}
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
		if len(low) != 1 || low[0].Details["regression"] != "fit" || low[0].Details["type"] != "REG_OLS" ||
			low[0].Details["min_required"] != 3 || low[0].Details["n_eff"].(float64) >= 3 {
			t.Fatalf("grouped=%v: low-n_eff warnings = %+v", grouped, low)
		}
		if _, err := run(types.WeightKindProbability, true); err == nil || !strings.Contains(err.Error(), "n_eff") {
			t.Fatalf("grouped=%v strict: %v", grouped, err)
		} else if ce, ok := err.(*errors.CodedError); !ok || ce.Code != errors.PULSE_WEIGHT_LOW_NEFF {
			t.Fatalf("grouped=%v strict code: %v", grouped, err)
		}
		resp, err = run(types.WeightKindFrequency, true)
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range resp.Warnings {
			if w.Code == string(errors.PULSE_WEIGHT_LOW_NEFF) {
				t.Fatalf("grouped=%v frequency warned: %+v", grouped, w)
			}
		}
	}
}

// TestWeightedRegressions_LowNEffAtFloor (U12 review WS-05): an OLS fit
// with n_eff EXACTLY p + 1 (weights 1, 1, 1, 3 → 36/12 = 3) has residual
// df 0, so it warns too (n_eff ≤ p + 1, not just <), naming the
// undefined figures.
func TestWeightedRegressions_LowNEffAtFloor(t *testing.T) {
	schema := regWeightSchema()
	recs := regWeightRecords(schema, []float64{1, 1, 1, 3})
	req := &types.Request{
		Weight:       &types.WeightSpec{Field: "w", Kind: types.WeightKindProbability},
		Regressions:  []*types.RegressionSpec{{Type: types.REG_OLS, Name: "fit", Target: "y", Predictors: []string{"x1", "x2"}}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x1", Weight: types.NullSlotWeight()}},
	}
	resp, err := NewProcessor(schema).Process(context.Background(), req, NewSliceIterator(recs))
	if err != nil {
		t.Fatal(err)
	}
	if r := resp.Regressions[0]; r.NEff != 3 || !math.IsNaN(r.ResidualStdErr) {
		t.Fatalf("n_eff %v residual_std_err %v, want 3 / NaN", r.NEff, r.ResidualStdErr)
	}
	var low []*types.ResponseWarning
	for _, w := range resp.Warnings {
		if w.Code == string(errors.PULSE_WEIGHT_LOW_NEFF) {
			low = append(low, w)
		}
	}
	if len(low) != 1 || low[0].Details["min_required"] != 3 || !strings.Contains(low[0].Message, "undefined") {
		t.Fatalf("low-n_eff warnings = %+v", low)
	}
}

// TestWeightedRegressions_GLMLowNEff: a probability-weighted REG_GLM
// below the p + 1 floor warns with GLM-specific prose (its Wald z has
// no residual df), on both fit paths.
func TestWeightedRegressions_GLMLowNEff(t *testing.T) {
	schema := regWeightSchema()
	recs := regWeightRecords(schema, []float64{1000, 1, 1, 1, 1, 1, 1})
	for _, grouped := range []bool{false, true} {
		req := &types.Request{
			Weight:       &types.WeightSpec{Field: "w", Kind: types.WeightKindProbability},
			Regressions:  []*types.RegressionSpec{{Type: types.REG_GLM, Family: "poisson", Name: "glm", Target: "y", Predictors: []string{"x1", "x2"}}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "x1", Weight: types.NullSlotWeight()}},
		}
		if grouped {
			req.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}}
		}
		resp, err := NewProcessor(schema).Process(context.Background(), req, NewSliceIterator(recs))
		if err != nil {
			t.Fatal(err)
		}
		if r := resp.Regressions[0]; r.NObs != len(recs) || r.NEff == 0 || r.NEff >= 3 {
			t.Fatalf("grouped=%v: n_obs %d n_eff %v", grouped, r.NObs, r.NEff)
		}
		var low []*types.ResponseWarning
		for _, w := range resp.Warnings {
			if w.Code == string(errors.PULSE_WEIGHT_LOW_NEFF) {
				low = append(low, w)
			}
		}
		if len(low) != 1 || low[0].Details["type"] != "REG_GLM" || !strings.Contains(low[0].Message, "Wald") {
			t.Fatalf("grouped=%v: low-n_eff warnings = %+v", grouped, low)
		}
	}
}
