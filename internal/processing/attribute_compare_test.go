package processing

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/vectors"
	"github.com/frankbardon/pulse/types"
)

// The comparison pair (U27 E3-S2) against hand oracles written apart
// from the linalg/metric kernels: pairwise-complete sums rescaled by
// p / p_observed, the fitted SDs and inverse covariance from the
// listwise (weighted) fit rows. Every case runs the streaming arm
// (single pass or the two-pass drive) and the buffered arm.

// cmpRows: x, y, z with nulls (NaN) on rows 3 (y), 7 (z) and 9 (all).
var cmpRows = [][3]float64{
	{1, 2, 0.5}, {2, 1, 1.5}, {3, 4, 0.2}, {4, math.NaN(), 2.2}, {5, 6, 1.1},
	{6, 4, 0.7}, {7, 8, 2.9}, {8, 9, math.NaN()}, {2.5, 7, 0.1}, {math.NaN(), math.NaN(), math.NaN()},
	{9, 2, 3.3}, {3, 3, 1},
}
var cmpGroups = []string{"a", "a", "a", "a", "b", "b", "b", "c", "c", "a", "b", "c"}
var cmpWeights = []float64{1, 2, 1, 3, 1, 2, 1, 1, 2, 1, 3, 1}

func cmpSchema() *encoding.Schema {
	s := mahaSchema()
	s.Fields = append(s.Fields, encoding.Field{Name: "w", Type: encoding.FieldTypeF64})
	return s
}

func cmpRecords(schema *encoding.Schema) []*Record {
	recs := mahaRecords(schema, cmpRows, cmpGroups)
	for i, r := range recs {
		r.Set("w", cmpWeights[i])
	}
	return recs
}

// pairs returns the complete pairs of x and r.
func cmpPairs(x, r []float64) (a, b []float64) {
	for i := range x {
		if !math.IsNaN(x[i]) && !math.IsNaN(r[i]) {
			a, b = append(a, x[i]), append(b, r[i])
		}
	}
	return a, b
}

func oracleEuclid(x, r []float64, sd []float64) float64 {
	n, s := 0, 0.0
	for i := range x {
		if math.IsNaN(x[i]) || math.IsNaN(r[i]) {
			continue
		}
		d := x[i] - r[i]
		if sd != nil {
			d /= sd[i]
		}
		s += d * d
		n++
	}
	if n == 0 {
		return math.NaN()
	}
	return math.Sqrt(s * float64(len(x)) / float64(n))
}

func oracleManhattan(x, r []float64) float64 {
	a, b := cmpPairs(x, r)
	if len(a) == 0 {
		return math.NaN()
	}
	s := 0.0
	for i := range a {
		s += math.Abs(a[i] - b[i])
	}
	return s * float64(len(x)) / float64(len(a))
}

func oracleCosine(x, r []float64) float64 {
	a, b := cmpPairs(x, r)
	if len(a) == 0 {
		return math.NaN()
	}
	var d, na, nb float64
	for i := range a {
		d += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	return d / math.Sqrt(na*nb)
}

func oracleCorrelation(x, r []float64) float64 {
	a, b := cmpPairs(x, r)
	if len(a) < 2 {
		return math.NaN()
	}
	var ma, mb float64
	for i := range a {
		ma += a[i]
		mb += b[i]
	}
	ma /= float64(len(a))
	mb /= float64(len(b))
	var sab, saa, sbb float64
	for i := range a {
		sab += (a[i] - ma) * (b[i] - mb)
		saa += (a[i] - ma) * (a[i] - ma)
		sbb += (b[i] - mb) * (b[i] - mb)
	}
	return sab / math.Sqrt(saa*sbb)
}

// cmpFit is the listwise fit over the complete rows (frequency weights
// when weighted): the means and the ddof = 1 covariance on Σw.
func cmpFit(weighted bool) (mean []float64, cov [3][3]float64) {
	var rows [][3]float64
	var ws []float64
	for i, r := range cmpRows {
		if math.IsNaN(r[0]) || math.IsNaN(r[1]) || math.IsNaN(r[2]) {
			continue
		}
		w := 1.0
		if weighted {
			w = cmpWeights[i]
		}
		rows, ws = append(rows, r), append(ws, w)
	}
	sw := 0.0
	mean = make([]float64, 3)
	for i, r := range rows {
		sw += ws[i]
		for j := range mean {
			mean[j] += ws[i] * r[j]
		}
	}
	for j := range mean {
		mean[j] /= sw
	}
	for i, r := range rows {
		for j := 0; j < 3; j++ {
			for k := 0; k < 3; k++ {
				cov[j][k] += ws[i] * (r[j] - mean[j]) * (r[k] - mean[k])
			}
		}
	}
	for j := 0; j < 3; j++ {
		for k := 0; k < 3; k++ {
			cov[j][k] /= sw - 1
		}
	}
	return mean, cov
}

func cmpSD(weighted bool) []float64 {
	_, cov := cmpFit(weighted)
	return []float64{math.Sqrt(cov[0][0]), math.Sqrt(cov[1][1]), math.Sqrt(cov[2][2])}
}

// runCmpArms runs build's request on the streaming arm (ungrouped) and
// the buffered arm (an ATTR_PERCENTILE forces it), asserts each arm ran,
// and returns each arm's per-row "out" (NaN = null).
func runCmpArms(t *testing.T, schema *encoding.Schema, build func() *types.Request) (streamed, buffered []float64) {
	t.Helper()
	var out [2][]float64
	for i, buf := range []bool{false, true} {
		recs := cmpRecords(schema)
		req := build()
		req.Aggregations = append(req.Aggregations, &types.Aggregation{Type: types.AGG_COUNT, Field: "g", Label: "n_rows", Weight: types.NullSlotWeight()})
		if buf {
			req.Attributes = append(req.Attributes, &types.Attribute{Type: types.ATTR_PERCENTILE, Field: "w", Label: "pct"})
		}
		p := NewProcessor(schema)
		if _, err := p.Process(context.Background(), req, NewSliceIterator(recs)); err != nil {
			t.Fatalf("buffered=%v: %v", buf, err)
		}
		if want := map[bool]ProcessPath{false: PathStreaming, true: PathBuffered}[buf]; p.LastPath() != want {
			t.Fatalf("buffered=%v ran %v, want %v", buf, p.LastPath(), want)
		}
		vals := make([]float64, len(recs))
		for k, rec := range recs {
			if v, ok := rec.NumericValue("out"); ok {
				vals[k] = v
			} else {
				vals[k] = math.NaN()
			}
		}
		out[i] = vals
	}
	return out[0], out[1]
}

func cmpClose(got, want float64) bool {
	if math.IsNaN(want) {
		return math.IsNaN(got)
	}
	return math.Abs(got-want) <= 1e-10*math.Max(1, math.Abs(want))
}

// TestComparison_RowSemantics: each metric, reference form and
// min_valid against the oracle, on the streaming arm (single pass for a
// row-local metric, the two-pass drive for a fitted one) and the
// buffered arm.
func TestComparison_RowSemantics(t *testing.T) {
	ref := []float64{2, 3, 1}
	sd, wsd := cmpSD(false), cmpSD(true)
	minValid := func(k int, f func(x []float64) float64) func(x []float64) float64 {
		return func(x []float64) float64 {
			if a, _ := cmpPairs(x, ref); len(a) < k {
				return math.NaN()
			}
			return f(x)
		}
	}
	cases := []struct {
		name   string
		typ    types.AttributeType
		params string
		weight bool
		oracle func(x []float64) float64
	}{
		{name: "euclidean literal", typ: types.ATTR_VEC_DISTANCE, params: `{"metric":"euclidean","reference":[2,3,1]}`,
			oracle: func(x []float64) float64 { return oracleEuclid(x, ref, nil) }},
		{name: "euclidean min_valid 3", typ: types.ATTR_VEC_DISTANCE, params: `{"metric":"euclidean","min_valid":3,"reference":[2,3,1]}`,
			oracle: minValid(3, func(x []float64) float64 { return oracleEuclid(x, ref, nil) })},
		{name: "manhattan label map", typ: types.ATTR_VEC_DISTANCE, params: `{"metric":"manhattan","reference":{"z":1,"x":2,"y":3}}`,
			oracle: func(x []float64) float64 { return oracleManhattan(x, ref) }},
		{name: "cosine distance", typ: types.ATTR_VEC_DISTANCE, params: `{"metric":"cosine","reference":[2,3,1]}`,
			oracle: func(x []float64) float64 { return 1 - oracleCosine(x, ref) }},
		{name: "cosine similarity", typ: types.ATTR_PROFILE_SIMILARITY, params: `{"metric":"cosine","reference":[2,3,1]}`,
			oracle: func(x []float64) float64 { return oracleCosine(x, ref) }},
		{name: "correlation min_valid 2", typ: types.ATTR_PROFILE_SIMILARITY, params: `{"metric":"correlation","min_valid":2,"reference":[2,3,1]}`,
			oracle: minValid(2, func(x []float64) float64 { return oracleCorrelation(x, ref) })},
		{name: "std_euclidean unweighted", typ: types.ATTR_VEC_DISTANCE, params: `{"metric":"std_euclidean","reference":[2,3,1]}`,
			oracle: func(x []float64) float64 { return oracleEuclid(x, ref, sd) }},
		{name: "std_euclidean weighted fit", typ: types.ATTR_VEC_DISTANCE, params: `{"metric":"std_euclidean","reference":[2,3,1]}`, weight: true,
			oracle: func(x []float64) float64 { return oracleEuclid(x, ref, wsd) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			schema := cmpSchema()
			streamed, buffered := runCmpArms(t, schema, func() *types.Request {
				req := &types.Request{Attributes: []*types.Attribute{{Type: c.typ, Label: "out", Fields: []string{"x", "y", "z"},
					Params: json.RawMessage(c.params)}}}
				if c.weight {
					req.Weight = &types.WeightSpec{Field: "w", Kind: types.WeightKindFrequency}
				}
				return req
			})
			checked := 0
			for i, row := range cmpRows {
				want := c.oracle(row[:])
				if !math.IsNaN(want) {
					checked++
				}
				if !cmpClose(streamed[i], want) || !cmpClose(buffered[i], want) {
					t.Errorf("row %d: streamed %v buffered %v, want %v", i, streamed[i], buffered[i], want)
				}
			}
			if checked < 8 {
				t.Fatalf("vacuous oracle: %d scored rows", checked)
			}
		})
	}
}

// TestComparison_MahalanobisMatchesAttrMahalanobis: the mahalanobis
// distance to the fit mean is √D² of ATTR_MAHALANOBIS on the same fit
// (complete rows; a row missing a member is null under both).
func TestComparison_MahalanobisMatchesAttrMahalanobis(t *testing.T) {
	schema := cmpSchema()
	mean, _ := cmpFit(false)
	refJSON, _ := json.Marshal(mean)
	recs := cmpRecords(schema)
	req := &types.Request{Attributes: []*types.Attribute{
		{Type: types.ATTR_VEC_DISTANCE, Label: "d", Fields: []string{"x", "y", "z"}, Params: json.RawMessage(`{"metric":"mahalanobis","reference":` + string(refJSON) + `}`)},
		{Type: types.ATTR_MAHALANOBIS, Label: "d2", Fields: []string{"x", "y", "z"}},
	}}
	p := NewProcessor(schema)
	if _, err := p.processRecords(context.Background(), p.prepare(req), recs); err != nil {
		t.Fatal(err)
	}
	scored := 0
	for i, r := range recs {
		d, dok := r.NumericValue("d")
		d2, d2ok := r.NumericValue("d2")
		if dok != d2ok {
			t.Fatalf("row %d: presence differs (%v, %v)", i, dok, d2ok)
		}
		if !dok {
			continue
		}
		scored++
		if math.Abs(d*d-d2) > 1e-9*math.Max(1, d2) {
			t.Errorf("row %d: d² = %v, ATTR_MAHALANOBIS D² = %v", i, d*d, d2)
		}
	}
	if scored < 8 {
		t.Fatalf("vacuous: %d rows scored", scored)
	}
}

// TestComparison_KeyFieldAndComponents: a {table, key_field} reference
// reads each row's own profile, aligned by label; a group with no
// profile (and a null member row) is null and counted in
// n_reference_missing, on both arms, with the floor beside it.
func TestComparison_KeyFieldAndComponents(t *testing.T) {
	tables := vectors.NewReferenceTables(map[string]types.ReferenceVectorTable{
		"t": {Labels: []string{"z", "y", "x"}, Profiles: map[string][]float64{"a": {1, 3, 2}, "b": {0, 0, 0}}},
	})
	profiles := map[string][]float64{"a": {2, 3, 1}, "b": {0, 0, 0}}
	for _, grouped := range []bool{false, true} {
		schema := cmpSchema()
		recs := cmpRecords(schema)
		req := &types.Request{Attributes: []*types.Attribute{{Type: types.ATTR_VEC_DISTANCE, Label: "out", Fields: []string{"x", "y", "z"},
			Params: json.RawMessage(`{"metric":"euclidean","reference":{"table":"t","key_field":"g"}}`)}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "out", Label: "n"}}}
		if grouped {
			req.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}}
		}
		p := NewProcessorWithExtensions(schema, &ExtensionRegistry{ReferenceVectors: tables})
		resp, err := p.Process(context.Background(), req, NewSliceIterator(recs))
		if err != nil {
			t.Fatal(err)
		}
		missing, n, nNull := 0, 0, 0
		for i, r := range recs {
			want := math.NaN()
			if prof, ok := profiles[cmpGroups[i]]; ok {
				want = oracleEuclid(cmpRows[i][:], prof, nil)
			} else {
				missing++
			}
			if math.IsNaN(want) {
				nNull++
			} else {
				n++
			}
			got, ok := r.NumericValue("out")
			if !ok {
				got = math.NaN()
			}
			if !cmpClose(got, want) {
				t.Errorf("grouped=%v row %d: %v, want %v", grouped, i, got, want)
			}
		}
		want := []types.AttributeComponents{{Label: "out", Type: types.ATTR_VEC_DISTANCE, N: n, NNull: nNull,
			Operator: map[string]any{"n_reference_missing": missing, "n_domain_invalid": 0}}}
		if resp.Components == nil || !reflect.DeepEqual(resp.Components.Attributes, want) {
			t.Errorf("grouped=%v: Components.Attributes = %+v, want %+v", grouped, resp.Components, want)
		}
		if missing == 0 {
			t.Fatal("vacuous: no row without a profile")
		}
	}
}

// TestComparison_DomainAndBuildRefusals: a value outside the metric's
// domain is null and counted (never a refusal); an unregistered table,
// an unknown key and labels that are not the vector's refuse the build
// with the coded error before any record.
func TestComparison_DomainAndBuildRefusals(t *testing.T) {
	schema := cmpSchema()
	recs := cmpRecords(schema)
	req := &types.Request{Attributes: []*types.Attribute{{Type: types.ATTR_VEC_DISTANCE, Label: "out", Fields: []string{"x", "y", "z"},
		Params: json.RawMessage(`{"metric":"hellinger","reference":[-1,1,1]}`)}}}
	p := NewProcessor(schema)
	resp, err := p.Process(context.Background(), req, NewSliceIterator(recs))
	if err != nil {
		t.Fatal(err)
	}
	op := resp.Components.Attributes[0].Operator
	if op["n_domain_invalid"] != len(recs)-1 { // the all-null row never reaches the metric
		t.Errorf("n_domain_invalid = %v, want %d", op["n_domain_invalid"], len(recs)-1)
	}

	tables := vectors.NewReferenceTables(map[string]types.ReferenceVectorTable{
		"t":     {Labels: []string{"x", "y", "z"}, Profiles: map[string][]float64{"k": {1, 2, 3}}},
		"other": {Labels: []string{"x", "y", "q"}, Profiles: map[string][]float64{"k": {1, 2, 3}}},
	})
	for ref, code := range map[string]errors.Code{
		`{"table":"nope","key":"k"}`:        errors.PULSE_VECTOR_REFERENCE_UNKNOWN,
		`{"table":"t","key":"zz"}`:          errors.PULSE_VECTOR_REFERENCE_UNKNOWN,
		`{"table":"other","key":"k"}`:       errors.PULSE_VECTOR_REFERENCE_MISMATCH,
		`{"table":"other","key_field":"g"}`: errors.PULSE_VECTOR_REFERENCE_MISMATCH,
	} {
		req := &types.Request{Attributes: []*types.Attribute{{Type: types.ATTR_PROFILE_SIMILARITY, Label: "out", Fields: []string{"x", "y", "z"},
			Params: json.RawMessage(`{"metric":"cosine","reference":` + ref + `}`)}}}
		p := NewProcessorWithExtensions(schema, &ExtensionRegistry{ReferenceVectors: tables})
		_, err := p.Process(context.Background(), req, NewSliceIterator(cmpRecords(schema)))
		ce, ok := err.(*errors.CodedError)
		if !ok || ce.Code != code {
			t.Errorf("%s: %v, want %s", ref, err, code)
		}
	}
}

// TestComparison_FittedIsTwoPassPerSlot: only ATTR_VEC_DISTANCE over a
// fitted metric (explicit, or a measure vector's default before the
// rewrite) takes the two-pass drive; the row-local metrics do not.
func TestComparison_FittedIsTwoPassPerSlot(t *testing.T) {
	specs := []types.VectorSpec{{Name: "m", Fields: []string{"x", "y"}, Kind: types.VectorKindMeasure}}
	for _, c := range []struct {
		attr *types.Attribute
		want bool
	}{
		{&types.Attribute{Type: types.ATTR_VEC_DISTANCE, Params: json.RawMessage(`{"metric":"std_euclidean"}`)}, true},
		{&types.Attribute{Type: types.ATTR_VEC_DISTANCE, Params: json.RawMessage(`{"metric":"mahalanobis"}`)}, true},
		{&types.Attribute{Type: types.ATTR_VEC_DISTANCE, Vector: "m", Params: json.RawMessage(`{"reference":[1,2]}`)}, true},
		{&types.Attribute{Type: types.ATTR_VEC_DISTANCE, Params: json.RawMessage(`{"metric":"euclidean"}`)}, false},
		{&types.Attribute{Type: types.ATTR_PROFILE_SIMILARITY, Params: json.RawMessage(`{"metric":"cosine"}`)}, false},
	} {
		var r *ExtensionRegistry
		if got := r.attrRequiresTwoPassIn(c.attr, specs); got != c.want {
			t.Errorf("%s %s: two-pass %v, want %v", c.attr.Type, c.attr.Params, got, c.want)
		}
	}
}
