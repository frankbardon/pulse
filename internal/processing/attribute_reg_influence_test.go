package processing

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// U65 E1-S2: the OLS refit's per-observation influence figures —
// ATTR_REG_RESIDUAL kind standardized / studentized and
// ATTR_REG_INFLUENCE measure cooks / dffits. The statsmodels oracle is
// the root ols_influence_reference_test.go; these pin the engine rules
// around it.

// influenceSlots are the four design-reading figures, as (type, params).
var influenceSlots = []struct {
	name   string
	typ    types.AttributeType
	params string
}{
	{"standardized", types.ATTR_REG_RESIDUAL, `{"kind":"standardized"}`},
	{"studentized", types.ATTR_REG_RESIDUAL, `{"kind":"studentized"}`},
	{"cooks", types.ATTR_REG_INFLUENCE, ``},
	{"dffits", types.ATTR_REG_INFLUENCE, `{"measure":"dffits"}`},
}

// computeInfluence runs one influence slot over recs (regWeightSchema)
// with weight as its stamped slot weight.
func computeInfluence(t *testing.T, typ types.AttributeType, params string, recs []*Record, weight types.SlotWeight) []float64 {
	t.Helper()
	spec := &types.Attribute{Type: typ, Target: "y", Predictors: []string{"x1", "x2"}, Weight: weight}
	if params != "" {
		spec.Params = json.RawMessage(params)
	}
	out, err := makeRegAttr(t, typ, regWeightSchema(), spec).Compute(recs, "")
	if err != nil {
		t.Fatalf("%s %s: %v", typ, params, err)
	}
	return out
}

// TestAttrRegInfluence_FrequencyIsExpansion: under integer frequency
// weights each record's figure is its first expanded copy's (one
// observation of the rep() expansion); unit weights are the unweighted
// figures bit for bit.
func TestAttrRegInfluence_FrequencyIsExpansion(t *testing.T) {
	t.Parallel()

	schema := regWeightSchema()
	recs := regWeightRecords(schema, attrRegWeights)
	var exp []*Record
	var first []int
	for i, r := range recs {
		first = append(first, len(exp))
		for k := 0; k < int(attrRegWeights[i]); k++ {
			exp = append(exp, r)
		}
	}
	ones := regWeightRecords(schema, []float64{1, 1, 1, 1, 1, 1, 1, 1, 1, 1})
	freq := weightOf(types.WeightKindFrequency)
	for _, s := range influenceSlots {
		got := computeInfluence(t, s.typ, s.params, recs, freq)
		want := computeInfluence(t, s.typ, s.params, exp, types.SlotWeight{})
		for i := range recs {
			if w := want[first[i]]; !closeRel(got[i], w, 1e-10) && math.Abs(got[i]-w) > 1e-12 {
				t.Errorf("%s row %d: %.17g, expansion %.17g", s.name, i, got[i], w)
			}
		}
		base := computeInfluence(t, s.typ, s.params, ones, types.SlotWeight{})
		unit := computeInfluence(t, s.typ, s.params, ones, freq)
		for i := range base {
			if unit[i] != base[i] {
				t.Fatalf("%s row %d: unit weight %.17g, unweighted %.17g", s.name, i, unit[i], base[i])
			}
		}
	}
}

// TestAttrRegInfluence_OutsideFitIsNull: a row with a null target, or
// one the weighted refit excludes (zero weight), is no observation of
// the fit — every figure is null (NaN), and the other rows' figures are
// the fit without it.
func TestAttrRegInfluence_OutsideFitIsNull(t *testing.T) {
	t.Parallel()

	schema := regWeightSchema()
	ws := append([]float64(nil), attrRegWeights...)
	ws[0] = 0
	recs := regWeightRecords(schema, ws)
	nullY := NewRecordWithNulls(schema, map[string]float64{"x1": 3, "x2": 1, "w": 1}, map[string]bool{"y": true})
	all := append(append([]*Record(nil), recs...), nullY)
	freq := weightOf(types.WeightKindFrequency)
	for _, s := range influenceSlots {
		got := computeInfluence(t, s.typ, s.params, all, freq)
		kept := computeInfluence(t, s.typ, s.params, recs[1:], freq)
		if !math.IsNaN(got[0]) || !math.IsNaN(got[len(got)-1]) {
			t.Errorf("%s: excluded row %v, null-target row %v, want NaN both", s.name, got[0], got[len(got)-1])
		}
		for i := 1; i < len(recs); i++ {
			if got[i] != kept[i-1] {
				t.Fatalf("%s row %d: %.17g, fit without the excluded row %.17g", s.name, i, got[i], kept[i-1])
			}
		}
	}
}

// TestAttrRegInfluence_UndefinedIsNull: with residual df = 1 the
// leave-one-out variance is undefined, so studentized and dffits are
// null while standardized and Cook's distance stay finite; at df = 0
// (n = p + 1, every h = 1) all four are null — never ±Inf.
func TestAttrRegInfluence_UndefinedIsNull(t *testing.T) {
	t.Parallel()

	schema := regWeightSchema()
	ones := []float64{1, 1, 1, 1, 1, 1, 1, 1, 1, 1}
	for _, n := range []int{3, 4} {
		recs := regWeightRecords(schema, ones)[:n]
		df := n - 3
		for _, s := range influenceSlots {
			got := computeInfluence(t, s.typ, s.params, recs, types.SlotWeight{})
			needsTwo := s.name == "studentized" || s.name == "dffits"
			for i, v := range got {
				if math.IsInf(v, 0) {
					t.Fatalf("df %d %s row %d: %v, want finite or NaN", df, s.name, i, v)
				}
				if undefined := df == 0 || (df == 1 && needsTwo); undefined != math.IsNaN(v) {
					t.Errorf("df %d %s row %d: %v, undefined %v", df, s.name, i, v, undefined)
				}
			}
		}
	}
}

// TestAttrRegInfluence_FactoryRefusals: the factory refuses a penalty
// and an unknown measure (PROCESSING_CONFIG, model.CheckRefit) and — a
// direct call the resolver would have refused first — a probability
// weight on a per-observation figure.
func TestAttrRegInfluence_FactoryRefusals(t *testing.T) {
	t.Parallel()

	schema := regWeightSchema()
	for name, spec := range map[string]*types.Attribute{
		"penalty":          {Type: types.ATTR_REG_INFLUENCE, Target: "y", Predictors: []string{"x1"}, Penalty: "l2", Alpha: 0.5},
		"kind penalty":     {Type: types.ATTR_REG_RESIDUAL, Target: "y", Predictors: []string{"x1"}, Penalty: "l1", Alpha: 0.5, Params: json.RawMessage(`{"kind":"studentized"}`)},
		"measure":          {Type: types.ATTR_REG_INFLUENCE, Target: "y", Predictors: []string{"x1"}, Params: json.RawMessage(`{"measure":"covratio"}`)},
		"probability":      {Type: types.ATTR_REG_INFLUENCE, Target: "y", Predictors: []string{"x1"}, Weight: weightOf(types.WeightKindProbability)},
		"kind probability": {Type: types.ATTR_REG_RESIDUAL, Target: "y", Predictors: []string{"x1"}, Weight: weightOf(types.WeightKindProbability), Params: json.RawMessage(`{"kind":"standardized"}`)},
	} {
		_, err := attributeRegistry[spec.Type](spec, schema)
		ce, ok := err.(*errors.CodedError)
		if !ok || ce.Code != errors.PROCESSING_CONFIG {
			t.Errorf("%s: err = %v, want PROCESSING_CONFIG", name, err)
			continue
		}
		if strings.Contains(name, "probability") && !strings.Contains(ce.Message, "set the slot's weight to null") {
			t.Errorf("%s: message %q names no fix", name, ce.Message)
		}
	}
	// A probability weight keeps the leverage and the plain residual.
	for _, typ := range []types.AttributeType{types.ATTR_REG_LEVERAGE, types.ATTR_REG_RESIDUAL} {
		spec := &types.Attribute{Type: typ, Target: "y", Predictors: []string{"x1"}, Weight: weightOf(types.WeightKindProbability)}
		if _, err := attributeRegistry[typ](spec, schema); err != nil {
			t.Errorf("%s under a probability weight: %v", typ, err)
		}
	}
}
