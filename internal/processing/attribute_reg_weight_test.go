package processing

import (
	"context"
	"math"
	"testing"

	"github.com/frankbardon/pulse/types"
)

// Weighted regression attributes (U12 E4-S2): ATTR_REG_FITTED /
// RESIDUAL / LEVERAGE refit REG_OLS with the slot's stamped weight and
// share its kinds. β is WLS (kind-free), the residual is raw y − ŷ and
// the leverage is the diagonal of W½X(XᵀWX)⁻¹XᵀW½ — R hatvalues() on a
// weighted lm, invariant to rescaling the weights.

// The external pins — stock R lm on the expanded rows (frequency:
// predict(), f × hatvalues()), the scale-free closed form cross-checked
// with statsmodels (probability) — are generated with every other
// weighted regression's into internal/service/weight_reference_values_test.go
// by testdata/weight_reference/gen_weight_reference.py
// (TestWeightReferenceValues/regressions).

// attrRegWeights: uneven integer weights over regWeightRecords' ten rows.
var attrRegWeights = []float64{1, 3, 2, 1, 4, 2, 1, 5, 1, 2}

var attrRegTypes = []types.AttributeType{types.ATTR_REG_FITTED, types.ATTR_REG_RESIDUAL, types.ATTR_REG_LEVERAGE}

// computeRegAttr runs one regression attribute over recs with weight
// (the zero SlotWeight: unweighted) set as its stamped slot weight.
func computeRegAttr(t *testing.T, typ types.AttributeType, recs []*Record, weight types.SlotWeight, penalty string) []float64 {
	t.Helper()
	spec := &types.Attribute{Type: typ, Target: "y", Predictors: []string{"x1", "x2"}, Weight: weight}
	if penalty != "" {
		spec.Penalty, spec.Alpha = penalty, 0.3
	}
	out, err := makeRegAttr(t, typ, regWeightSchema(), spec).Compute(recs, "")
	if err != nil {
		t.Fatalf("%s: %v", typ, err)
	}
	return out
}

func weightOf(kind types.WeightKind) types.SlotWeight {
	return types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: kind})
}

var bothKinds = []types.WeightKind{types.WeightKindFrequency, types.WeightKindProbability}

// TestAttrRegWeighted_UnityBitIdentical: all-ones weights under either
// kind reproduce the unweighted outputs bit for bit (penalised fitted
// included).
func TestAttrRegWeighted_UnityBitIdentical(t *testing.T) {
	schema := regWeightSchema()
	ones := []float64{1, 1, 1, 1, 1, 1, 1, 1, 1, 1}
	recs := regWeightRecords(schema, ones)
	for _, typ := range attrRegTypes {
		for _, penalty := range []string{"", "l2"} {
			if penalty != "" && typ == types.ATTR_REG_LEVERAGE {
				continue
			}
			base := computeRegAttr(t, typ, recs, types.SlotWeight{}, penalty)
			for _, kind := range bothKinds {
				got := computeRegAttr(t, typ, recs, weightOf(kind), penalty)
				for i := range base {
					if got[i] != base[i] {
						t.Fatalf("%s%s/%s row %d: %.17g, unweighted %.17g", typ, penalty, kind, i, got[i], base[i])
					}
				}
			}
		}
	}
}

// TestAttrRegWeighted_FrequencyIsExpansion: integer frequency weights
// give each row the fitted value / residual of its expanded copies and
// w times a copy's leverage (the copies share the row's hat mass).
func TestAttrRegWeighted_FrequencyIsExpansion(t *testing.T) {
	schema := regWeightSchema()
	recs := regWeightRecords(schema, attrRegWeights)
	var exp []*Record
	var first []int // index of each row's first copy
	for i, r := range recs {
		first = append(first, len(exp))
		for k := 0; k < int(attrRegWeights[i]); k++ {
			exp = append(exp, r)
		}
	}
	for _, typ := range attrRegTypes {
		got := computeRegAttr(t, typ, recs, weightOf(types.WeightKindFrequency), "")
		want := computeRegAttr(t, typ, exp, types.SlotWeight{}, "")
		for i := range recs {
			w := want[first[i]]
			if typ == types.ATTR_REG_LEVERAGE {
				w *= attrRegWeights[i]
			}
			if !closeRel(got[i], w, 1e-10) && math.Abs(got[i]-w) > 1e-12 {
				t.Errorf("%s row %d: %.17g, expansion %.17g", typ, i, got[i], w)
			}
		}
	}
}

// TestAttrRegWeighted_ExcludedRows: a zero or invalid weight keeps the
// row out of the refit (the fit equals the one without it) while it
// still gets its fitted value / residual; its leverage is 0.
func TestAttrRegWeighted_ExcludedRows(t *testing.T) {
	schema := regWeightSchema()
	ws := append([]float64(nil), attrRegWeights...)
	ws[0], ws[1] = 0, -1
	recs := regWeightRecords(schema, ws)
	for _, typ := range attrRegTypes {
		got := computeRegAttr(t, typ, recs, weightOf(types.WeightKindProbability), "")
		kept := computeRegAttr(t, typ, recs[2:], weightOf(types.WeightKindProbability), "")
		for i := 2; i < len(recs); i++ {
			if got[i] != kept[i-2] {
				t.Fatalf("%s row %d: %.17g, fit without excluded rows %.17g", typ, i, got[i], kept[i-2])
			}
		}
		if typ == types.ATTR_REG_LEVERAGE && (got[0] != 0 || got[1] != 0) {
			t.Fatalf("excluded rows' leverage %v / %v, want 0", got[0], got[1])
		}
		if typ == types.ATTR_REG_FITTED && got[0] == 0 {
			t.Fatalf("excluded row lost its fitted value")
		}
	}
}

// TestAttrRegWeighted_WeightReachesRefit: through the orchestrator, a
// slot weight and a request weight both reach the refit (stamped) on
// the streaming and buffered paths; `weight: null` opts out.
func TestAttrRegWeighted_WeightReachesRefit(t *testing.T) {
	schema := regWeightSchema()
	recs := regWeightRecords(schema, attrRegWeights)
	wantSum := 0.0
	for _, v := range computeRegAttr(t, types.ATTR_REG_FITTED, recs, weightOf(types.WeightKindFrequency), "") {
		wantSum += v
	}
	unweightedSum := 0.0
	for _, r := range recs {
		y, _ := r.NumericValue("y")
		unweightedSum += y // OLS with an intercept: Σŷ = Σy
	}
	freq := &types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency}
	for name, tc := range map[string]struct {
		reqW *types.WeightSpec
		slot types.SlotWeight
		want float64
	}{
		"slot":      {nil, types.SlotWeightField("w"), wantSum},
		"request":   {freq, types.SlotWeight{}, wantSum},
		"opted_out": {freq, types.NullSlotWeight(), unweightedSum},
	} {
		req := &types.Request{Weight: tc.reqW,
			Attributes:   []*types.Attribute{{Type: types.ATTR_REG_FITTED, Target: "y", Predictors: []string{"x1", "x2"}, Label: "fit", Weight: tc.slot}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "fit", Label: "sum_fit", Weight: types.NullSlotWeight()}},
		}
		stream, buf := runStreamingAndBuffered(t, schema, recs, StampWeights(req, nil))
		for arm, resp := range map[string]*types.Response{"streaming": stream, "buffered": buf} {
			got, _ := resp.Data[0]["sum_fit"].(float64)
			if !closeRel(got, tc.want, 1e-9) {
				t.Errorf("%s/%s: Σ fitted = %.17g, want %.17g", name, arm, got, tc.want)
			}
		}
		// The public entry point stamps itself.
		resp, err := NewProcessor(schema).Process(context.Background(), req, NewSliceIterator(regWeightRecords(schema, attrRegWeights)))
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := resp.Data[0]["sum_fit"].(float64); !closeRel(got, tc.want, 1e-9) {
			t.Errorf("%s/Process: Σ fitted = %.17g, want %.17g", name, got, tc.want)
		}
	}
}

func closeRel(a, b, tol float64) bool {
	if a == b {
		return true
	}
	return math.Abs(a-b) <= tol*math.Max(math.Abs(a), math.Abs(b))
}
