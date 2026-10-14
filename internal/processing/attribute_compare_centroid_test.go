package processing

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/model"
	"github.com/frankbardon/pulse/types"
)

// Centroid and population references (U30 E3-S1) against hand oracles:
// the centroid is the per-element weighted mean Σw·x_i / Σw over the
// rows the centroid filter admits whose member i is present (AGG_VEC_MEAN's
// rule), one per by value when by is set.

// cmpCentroids is the oracle: per key (by = cmpGroups; "" for a by-less
// centroid) the per-element mean over rows admit admits.
func cmpCentroids(admit func(i int) bool, weighted, byGroup bool) map[string][]float64 {
	sum, sw := map[string]*[3]float64{}, map[string]*[3]float64{}
	for i, r := range cmpRows {
		if !admit(i) {
			continue
		}
		k := ""
		if byGroup {
			k = cmpGroups[i]
		}
		w := 1.0
		if weighted {
			w = cmpWeights[i]
		}
		for j, x := range r {
			if math.IsNaN(x) {
				continue
			}
			if sum[k] == nil {
				sum[k], sw[k] = &[3]float64{}, &[3]float64{}
			}
			sum[k][j] += w * x
			sw[k][j] += w
		}
	}
	out := map[string][]float64{}
	for k := range sum {
		c := make([]float64, 3)
		for j := range c {
			c[j] = math.NaN()
			if sw[k][j] > 0 {
				c[j] = sum[k][j] / sw[k][j]
			}
		}
		out[k] = c
	}
	return out
}

// TestComparison_CentroidRows: each centroid form (all rows, a filter,
// by, filter + by; weighted and not; a row-local and a fitted metric)
// scores every row against the oracle's centroid on the streaming
// two-pass drive and the buffered arm, the two arms bit-identical.
func TestComparison_CentroidRows(t *testing.T) {
	all := func(int) bool { return true }
	inA := func(i int) bool { return cmpGroups[i] == "a" }
	notC := func(i int) bool { return cmpGroups[i] != "c" }
	sd, wsd := cmpSD(false), cmpSD(true)
	cases := []struct {
		name     string
		typ      types.AttributeType
		params   string
		weighted bool
		admit    func(int) bool
		by       bool
		score    func(x, c []float64, weighted bool) float64
	}{
		{name: "similarity centroid of all rows", typ: types.ATTR_PROFILE_SIMILARITY, params: `{"metric":"cosine","reference":{"centroid":{}}}`, admit: all,
			score: func(x, c []float64, _ bool) float64 { return oracleCosine(x, c) }},
		{name: "similarity weighted centroid", typ: types.ATTR_PROFILE_SIMILARITY, params: `{"metric":"correlation","reference":{"centroid":{}}}`, admit: all, weighted: true,
			score: func(x, c []float64, _ bool) float64 { return oracleCorrelation(x, c) }},
		{name: "distance filtered centroid weighted", typ: types.ATTR_VEC_DISTANCE,
			params: `{"metric":"euclidean","reference":{"centroid":{"filter":[{"type":"FILTER_INCLUDE","field":"g","values":["a"]}]}}}`, admit: inA, weighted: true,
			score: func(x, c []float64, _ bool) float64 { return oracleEuclid(x, c, nil) }},
		{name: "distance by group", typ: types.ATTR_VEC_DISTANCE, params: `{"metric":"manhattan","reference":{"centroid":{"by":"g"}}}`, admit: all, by: true,
			score: func(x, c []float64, _ bool) float64 { return oracleManhattan(x, c) }},
		{name: "distance by group weighted, filtered", typ: types.ATTR_VEC_DISTANCE,
			params: `{"metric":"euclidean","reference":{"centroid":{"by":"g","filter":[{"type":"FILTER_EXCLUDE","field":"g","values":["c"]}]}}}`, admit: notC, weighted: true, by: true,
			score: func(x, c []float64, _ bool) float64 { return oracleEuclid(x, c, nil) }},
		{name: "fitted metric over a weighted centroid", typ: types.ATTR_VEC_DISTANCE, params: `{"metric":"std_euclidean","reference":{"centroid":{}}}`, admit: all, weighted: true,
			score: func(x, c []float64, w bool) float64 {
				if w {
					return oracleEuclid(x, c, wsd)
				}
				return oracleEuclid(x, c, sd)
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			schema := cmpSchema()
			streamed, buffered := runCmpArms(t, schema, func() *types.Request {
				req := &types.Request{Attributes: []*types.Attribute{{Type: c.typ, Label: "out", Fields: []string{"x", "y", "z"},
					Params: json.RawMessage(c.params)}}}
				if c.weighted {
					req.Weight = &types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency}
				}
				return req
			})
			cents := cmpCentroids(c.admit, c.weighted, c.by)
			checked, missing := 0, 0
			for i, row := range cmpRows {
				k := ""
				if c.by {
					k = cmpGroups[i]
				}
				want := math.NaN()
				if cent, ok := cents[k]; ok {
					want = c.score(row[:], cent, c.weighted)
				} else {
					missing++
				}
				if !math.IsNaN(want) {
					checked++
				}
				if !cmpClose(streamed[i], want) || !cmpClose(buffered[i], want) {
					t.Errorf("row %d: streamed %v buffered %v, want %v", i, streamed[i], buffered[i], want)
				}
				if math.Float64bits(streamed[i]) != math.Float64bits(buffered[i]) && !(math.IsNaN(streamed[i]) && math.IsNaN(buffered[i])) {
					t.Errorf("row %d: streamed %v and buffered %v differ in bits", i, streamed[i], buffered[i])
				}
			}
			if checked < 6 {
				t.Fatalf("vacuous oracle: %d scored rows", checked)
			}
			if c.by && c.admit(7) == false && missing == 0 {
				t.Fatal("vacuous: the filtered by-centroid misses no row")
			}
		})
	}
}

// TestComparison_CentroidIsAggVecMean: the centroid a slot folds is
// AGG_VEC_MEAN's figure over the same rows to the bit — unweighted and
// under frequency and probability weights.
func TestComparison_CentroidIsAggVecMean(t *testing.T) {
	for _, kind := range []types.WeightKind{"", types.WeightKindFrequency, types.WeightKindProbability} {
		t.Run(string(kind)+"_", func(t *testing.T) {
			schema := cmpSchema()
			recs := cmpRecords(schema)
			var weight types.SlotWeight
			if kind != "" {
				weight = types.SlotWeightOf(types.WeightSpec{Field: "w", Kind: kind})
			}
			attr := &types.Attribute{Type: types.ATTR_PROFILE_SIMILARITY, Label: "s", Fields: []string{"x", "y", "z"}, Weight: weight,
				Params: json.RawMessage(`{"metric":"cosine","reference":{"centroid":{}}}`)}
			computer, err := newComparisonAttribute(attr, schema)
			if err != nil {
				t.Fatal(err)
			}
			if err := BindAttribute(computer, nil, nil); err != nil {
				t.Fatal(err)
			}
			cc := computer.(*centroidComparisonAttribute)
			if _, err := cc.Compute(recs, "s"); err != nil {
				t.Fatal(err)
			}
			agg, err := newVecAggregator(&types.Aggregation{Type: types.AGG_VEC_MEAN, Fields: []string{"x", "y", "z"}, Weight: weight}, schema)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := agg.Aggregate(cmpRecords(schema), ""); err != nil {
				t.Fatal(err)
			}
			rich, err := agg.(*vecAggregator).Rich()
			if err != nil {
				t.Fatal(err)
			}
			want := rich.([]float64)
			for i := range want {
				if math.Float64bits(cc.static[i]) != math.Float64bits(want[i]) {
					t.Errorf("element %d: centroid %v, AGG_VEC_MEAN %v", i, cc.static[i], want[i])
				}
			}
			if kind != "" && reflect.DeepEqual(cc.static, cmpCentroids(func(int) bool { return true }, false, false)[""]) {
				t.Error("vacuous: the weighted centroid equals the unweighted one")
			}
		})
	}
}

// TestComparison_CentroidMissing: a by value no centroid row reached (a
// filter excluding it) and a null by are the existing miss — null,
// counted in n_reference_missing, one PULSE_VECTOR_REFERENCE_MISSING
// naming by — never a refusal; a by-less centroid no row reached nulls
// every row with the same warning.
func TestComparison_CentroidMissing(t *testing.T) {
	schema := cmpSchema()
	schema.Fields[3].Nullable = true // g
	const nullRow = 5
	unmatched := 0
	for i, g := range cmpGroups {
		if i != nullRow && g == "c" {
			unmatched++
		}
	}
	for _, c := range []struct {
		name, params string
		details      map[string]any
	}{
		{name: "by", params: `{"metric":"euclidean","reference":{"centroid":{"by":"g","filter":[{"type":"FILTER_EXCLUDE","field":"g","values":["c"]}]}}}`,
			details: map[string]any{"attribute": "out", "type": "ATTR_VEC_DISTANCE", "reference": "centroid", "by": "g",
				"n_reference_missing": unmatched + 1, "n_key_null": 1, "n_key_unmatched": unmatched, "keys": []string{"c"}}},
		{name: "empty centroid", params: `{"metric":"euclidean","reference":{"centroid":{"filter":[{"type":"FILTER_EXPRESSION","expression":"x > 100"}]}}}`,
			details: map[string]any{"attribute": "out", "type": "ATTR_VEC_DISTANCE", "reference": "centroid", "n_reference_missing": len(cmpRows)}},
	} {
		for _, buffered := range []bool{false, true} {
			t.Run(c.name, func(t *testing.T) {
				recs := cmpRecords(schema)
				recs[nullRow].SetNullField("g")
				req := &types.Request{Attributes: []*types.Attribute{{Type: types.ATTR_VEC_DISTANCE, Label: "out", Fields: []string{"x", "y", "z"}, Params: json.RawMessage(c.params)}},
					Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "out", Label: "n"}}}
				if buffered {
					req.Attributes = append(req.Attributes, &types.Attribute{Type: types.ATTR_PERCENTILE, Field: "w", Label: "pct"})
				}
				p := NewProcessor(schema)
				resp, err := p.Process(context.Background(), req, NewSliceIterator(recs))
				if err != nil {
					t.Fatal(err)
				}
				var got []*types.ResponseWarning
				for _, w := range resp.Warnings {
					if w.Code == string(errors.PULSE_VECTOR_REFERENCE_MISSING) {
						got = append(got, w)
					}
				}
				if len(got) != 1 || !reflect.DeepEqual(got[0].Details, c.details) {
					t.Fatalf("buffered=%v warnings %+v, want one with %v", buffered, got, c.details)
				}
				if op := resp.Components.Attributes[0].Operator; op["n_reference_missing"] != c.details["n_reference_missing"] {
					t.Errorf("components %+v", op)
				}
			})
		}
	}
}

// TestComparison_PopulationReference: {population} reads the bound
// fit's stored mean aligned by label (the fit stored in another axis
// order scores identically), for a row-local and a fitted metric; an
// unbound name is PULSE_VECTOR_REFERENCE_UNKNOWN and a fit over other
// labels PULSE_VECTOR_REFERENCE_MISMATCH, at build.
func TestComparison_PopulationReference(t *testing.T) {
	mean := []float64{2, 3, 1}
	vf := &model.VectorFit{Name: "pop", Labels: []string{"x", "y", "z"}, Members: []string{"x", "y", "z"}, Basis: model.VectorBasisUnweighted,
		N: 4, NMass: 4, SumWeights: 4, SumWeightsSq: 4, Mean: mean, M2: [][]float64{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}}}
	rev := reversedFit(vf)
	other := *vf
	other.Name, other.Labels = "other", []string{"x", "y", "q"}
	sd := cmpSD(false)
	for _, c := range []struct {
		name, params string
		fit          *model.VectorFit
		oracle       func(x []float64) float64
		code         errors.Code
	}{
		{name: "similarity", params: `{"metric":"cosine","reference":{"population":"pop"}}`, fit: vf, oracle: func(x []float64) float64 { return oracleCosine(x, mean) }},
		{name: "reversed storage aligns by label", params: `{"metric":"cosine","reference":{"population":"pop"}}`, fit: rev, oracle: func(x []float64) float64 { return oracleCosine(x, mean) }},
		{name: "fitted metric fits this request", params: `{"metric":"std_euclidean","reference":{"population":"pop"}}`, fit: vf, oracle: func(x []float64) float64 { return oracleEuclid(x, mean, sd) }},
		{name: "unbound", params: `{"metric":"cosine","reference":{"population":"nope"}}`, fit: vf, code: errors.PULSE_VECTOR_REFERENCE_UNKNOWN},
		{name: "labels differ", params: `{"metric":"cosine","reference":{"population":"other"}}`, fit: vf, code: errors.PULSE_VECTOR_REFERENCE_MISMATCH},
	} {
		t.Run(c.name, func(t *testing.T) {
			typ := types.ATTR_PROFILE_SIMILARITY
			if c.name == "fitted metric fits this request" {
				typ = types.ATTR_VEC_DISTANCE
			}
			schema := cmpSchema()
			recs := cmpRecords(schema)
			req := &types.Request{Attributes: []*types.Attribute{{Type: typ, Label: "out", Fields: []string{"x", "y", "z"}, Params: json.RawMessage(c.params)}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "out", Label: "n"}}}
			p := NewProcessor(schema)
			set := modelSetOf(t, c.fit, &other)
			p.SetModels(set)
			_, err := p.Process(context.Background(), req, NewSliceIterator(recs))
			if c.code != "" {
				ce, ok := err.(*errors.CodedError)
				if !ok || ce.Code != c.code || ce.Details["population"] == nil {
					t.Fatalf("Process: %v, want %s naming the population", err, c.code)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			scored := 0
			for i, r := range recs {
				got, ok := r.NumericValue("out")
				if !ok {
					got = math.NaN()
				}
				want := c.oracle(cmpRows[i][:])
				if !math.IsNaN(want) {
					scored++
				}
				if !cmpClose(got, want) {
					t.Errorf("row %d: %v, want %v", i, got, want)
				}
			}
			if scored < 8 {
				t.Fatalf("vacuous: %d rows scored", scored)
			}
		})
	}
}

// TestComparison_CentroidTwoPassPerSlot: a centroid makes either type
// two-pass whatever the metric — beside a bound fit too — while a
// population is row-local for a row-local metric.
func TestComparison_CentroidTwoPassPerSlot(t *testing.T) {
	for _, c := range []struct {
		attr *types.Attribute
		want bool
	}{
		{&types.Attribute{Type: types.ATTR_PROFILE_SIMILARITY, Params: json.RawMessage(`{"metric":"cosine","reference":{"centroid":{}}}`)}, true},
		{&types.Attribute{Type: types.ATTR_VEC_DISTANCE, Params: json.RawMessage(`{"metric":"euclidean","reference":{"centroid":{"by":"g"}}}`)}, true},
		{&types.Attribute{Type: types.ATTR_VEC_DISTANCE, Params: json.RawMessage(`{"metric":"mahalanobis","fit":"pop","reference":{"centroid":{}}}`)}, true},
		{&types.Attribute{Type: types.ATTR_VEC_DISTANCE, Params: json.RawMessage(`{"metric":"mahalanobis","fit":"pop","reference":[1,2]}`)}, false},
		{&types.Attribute{Type: types.ATTR_PROFILE_SIMILARITY, Params: json.RawMessage(`{"metric":"cosine","reference":{"population":"pop"}}`)}, false},
	} {
		var r *ExtensionRegistry
		if got := r.attrRequiresTwoPassIn(c.attr, nil, nil); got != c.want {
			t.Errorf("%s %s: two-pass %v, want %v", c.attr.Type, c.attr.Params, got, c.want)
		}
	}
}
