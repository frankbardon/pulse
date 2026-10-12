package processing

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strconv"
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

// TestComparison_RowWarnings (E3-S4): a row nulled for a missing
// reference (a null key, or a key the table lacks) or a domain violation
// raises one coded Response warning per slot — label, type and counts in
// details, null and unmatched keys apart — on the streaming, buffered
// and fitted (two-pass) arms, components on or off; a clean run raises
// none. An unsigned-integer key_field matches by its base-10 form and
// scores exactly as its categorical twin.
func TestComparison_RowWarnings(t *testing.T) {
	schema := cmpSchema()
	schema.Fields[3].Nullable = true // g
	schema.Fields = append(schema.Fields,
		encoding.Field{Name: "gid", Type: encoding.FieldTypeU8, Nullable: true},
		encoding.Field{Name: "gbig", Type: encoding.FieldTypeU64, Nullable: true})
	ids := map[string]float64{"a": 1, "b": 2, "c": 30}
	const nullRow = 5
	records := func() []*Record {
		recs := cmpRecords(schema)
		for i, r := range recs {
			if i == nullRow {
				r.SetNullField("g")
				continue
			}
			r.Set("gid", ids[cmpGroups[i]])
			r.Set("gbig", ids[cmpGroups[i]]+1e12)
		}
		return recs
	}
	tables := vectors.NewReferenceTables(map[string]types.ReferenceVectorTable{
		"t": {Labels: []string{"x", "y", "z"}, Profiles: map[string][]float64{
			"a": {1, 3, 2}, "b": {0, 1, 0}, "1": {1, 3, 2}, "2": {0, 1, 0}, "01": {9, 9, 9},
			"1000000000001": {1, 3, 2}, "1000000000002": {0, 1, 0}}},
	})
	unmatched := 0
	for i, g := range cmpGroups {
		if i != nullRow && g == "c" {
			unmatched++
		}
	}
	missing := map[string]any{"n_reference_missing": unmatched + 1, "n_key_null": 1, "n_key_unmatched": unmatched}
	cases := []struct {
		name, typ, params string
		code              errors.Code
		details           map[string]any
		twin              string // the categorical key_field twin's params
	}{
		{name: "categorical key", typ: "dist", params: `{"metric":"euclidean","reference":{"table":"t","key_field":"g"}}`,
			code: errors.PULSE_VECTOR_REFERENCE_MISSING, details: merge(missing, map[string]any{"key_field": "g", "keys": []string{"c"}})},
		{name: "u8 key", typ: "dist", params: `{"metric":"euclidean","reference":{"table":"t","key_field":"gid"}}`,
			code: errors.PULSE_VECTOR_REFERENCE_MISSING, details: merge(missing, map[string]any{"key_field": "gid", "keys": []string{"30"}}),
			twin: `{"metric":"euclidean","reference":{"table":"t","key_field":"g"}}`},
		{name: "u64 key", typ: "sim", params: `{"metric":"cosine","reference":{"table":"t","key_field":"gbig"}}`,
			code: errors.PULSE_VECTOR_REFERENCE_MISSING, details: merge(missing, map[string]any{"key_field": "gbig", "keys": []string{"1000000000030"}}),
			twin: `{"metric":"cosine","reference":{"table":"t","key_field":"g"}}`},
		{name: "fitted key", typ: "dist", params: `{"metric":"std_euclidean","reference":{"table":"t","key_field":"gid"}}`,
			code: errors.PULSE_VECTOR_REFERENCE_MISSING, details: merge(missing, map[string]any{"key_field": "gid", "keys": []string{"30"}}),
			twin: `{"metric":"std_euclidean","reference":{"table":"t","key_field":"g"}}`},
		{name: "domain", typ: "dist", params: `{"metric":"hellinger","reference":[-1,1,1]}`,
			code: errors.PULSE_VECTOR_METRIC_DOMAIN, details: map[string]any{"metric": "hellinger", "n_domain_invalid": len(cmpRows) - 1, "reasons": []string{"negative"}}},
		{name: "clean", typ: "sim", params: `{"metric":"cosine","reference":[1,2,3]}`},
	}
	run := func(t *testing.T, typ, params string, buffered, noComponents, twoPass bool) (*types.Response, []float64) {
		at := map[string]types.AttributeType{"dist": types.ATTR_VEC_DISTANCE, "sim": types.ATTR_PROFILE_SIMILARITY}[typ]
		req := &types.Request{Attributes: []*types.Attribute{{Type: at, Label: "out", Fields: []string{"x", "y", "z"}, Params: json.RawMessage(params)}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "out", Label: "n"}}}
		if buffered {
			req.Attributes = append(req.Attributes, &types.Attribute{Type: types.ATTR_PERCENTILE, Field: "w", Label: "pct"})
		}
		if twoPass {
			// A two-pass stage reading the score re-scores the row-local
			// slot on the prepass: the tally must count each row once.
			req.Attributes = append(req.Attributes, &types.Attribute{Type: types.ATTR_ZSCORE, Field: "out", Label: "z"})
		}
		recs := records()
		p := NewProcessorWithExtensions(schema, &ExtensionRegistry{ReferenceVectors: tables})
		p.SetDisableComponents(noComponents)
		resp, err := p.Process(context.Background(), req, NewSliceIterator(recs))
		if err != nil {
			t.Fatal(err)
		}
		vals := make([]float64, len(recs))
		for i, r := range recs {
			v, ok := r.NumericValue("out")
			if !ok {
				v = math.NaN()
			}
			vals[i] = v
		}
		return resp, vals
	}
	for _, c := range cases {
		for _, arm := range []struct {
			name                            string
			buffered, noComponents, twoPass bool
		}{{"streaming", false, false, false}, {"buffered", true, false, false}, {"no components", false, true, false}, {"two-pass", false, false, true}} {
			t.Run(c.name+"/"+arm.name, func(t *testing.T) {
				resp, vals := run(t, c.typ, c.params, arm.buffered, arm.noComponents, arm.twoPass)
				var got []*types.ResponseWarning
				for _, w := range resp.Warnings {
					if w.Code == string(errors.PULSE_VECTOR_REFERENCE_MISSING) || w.Code == string(errors.PULSE_VECTOR_METRIC_DOMAIN) {
						got = append(got, w)
					}
				}
				if c.code == "" {
					if len(got) != 0 {
						t.Fatalf("clean run warns %+v", got)
					}
					return
				}
				if len(got) != 1 || got[0].Code != string(c.code) {
					t.Fatalf("warnings %+v, want one %s", resp.Warnings, c.code)
				}
				at := map[string]string{"dist": "ATTR_VEC_DISTANCE", "sim": "ATTR_PROFILE_SIMILARITY"}[c.typ]
				want := merge(c.details, map[string]any{"attribute": "out", "type": at})
				if c.code == errors.PULSE_VECTOR_REFERENCE_MISSING {
					want["table"] = "t"
				}
				if !reflect.DeepEqual(got[0].Details, want) {
					t.Errorf("details %#v, want %#v", got[0].Details, want)
				}
				if arm.noComponents != (resp.Components == nil) {
					t.Errorf("components %v with DisableComponents %v", resp.Components, arm.noComponents)
				}
				if c.twin != "" {
					_, twin := run(t, c.typ, c.twin, arm.buffered, arm.noComponents, arm.twoPass)
					scored := 0
					for i := range vals {
						if !cmpClose(vals[i], twin[i]) {
							t.Errorf("row %d: %v, categorical twin %v", i, vals[i], twin[i])
						}
						if !math.IsNaN(vals[i]) {
							scored++
						}
					}
					if scored < len(vals)/2 {
						t.Fatalf("vacuous: %d rows scored", scored)
					}
				}
			})
		}
	}
}

func merge(a, b map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

// TestComparison_UnmatchedKeysSmallestFirst: the warning lists the
// comparisonUnmatchedKeys smallest distinct unmatched values whatever
// the row order.
func TestComparison_UnmatchedKeysSmallestFirst(t *testing.T) {
	var keys []string
	for i := 0; i < 25; i++ {
		keys = append(keys, strconv.Itoa(100+i))
	}
	want := slices.Clone(keys[:comparisonUnmatchedKeys])
	for _, order := range [][]string{keys, reversed(keys), append(slices.Clone(keys[12:]), keys[:12]...)} {
		var c comparisonCore
		for _, k := range order {
			c.noteUnmatched(k)
			c.noteUnmatched(k)
		}
		if !reflect.DeepEqual(c.unmatched, want) {
			t.Errorf("unmatched = %v, want %v", c.unmatched, want)
		}
	}
}

func reversed(s []string) []string {
	out := slices.Clone(s)
	slices.Reverse(out)
	return out
}
