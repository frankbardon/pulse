package processing

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// scaleSchema: four f64 battery items q1..q4 and a grouping column g.
func scaleSchema() *encoding.Schema {
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "q1", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "q2", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "q3", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "q4", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "g", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary()},
	}}
}

var nan = math.NaN()

// scaleRows: r0 complete, r1 one missing, r2 three missing, r3 an
// out-of-range 9 on a 1..5 scale, r4 constant.
var scaleRows = [][4]float64{
	{1, 2, 3, 4},
	{5, nan, 1, 2},
	{nan, nan, nan, 3},
	{9, 1, 2, 3},
	{1, 1, 1, 1},
}

func scaleRecords(schema *encoding.Schema) []*Record {
	recs := make([]*Record, len(scaleRows))
	for i, r := range scaleRows {
		vals := map[string]float64{"g": float64(dictIDOrAdd(schema, "g", []string{"a", "b"}[i%2]))}
		for j, name := range []string{"q1", "q2", "q3", "q4"} {
			if !math.IsNaN(r[j]) {
				vals[name] = r[j]
			}
		}
		recs[i] = NewRecord(schema, vals)
	}
	return recs
}

func scaleVector(kindBounds bool, lo, hi float64) []types.VectorSpec {
	v := types.VectorSpec{Name: "bat", Fields: []string{"q1", "q2", "q3", "q4"}, Labels: []string{"a", "b", "c", "d"}}
	if kindBounds {
		v.Kind, v.ScaleMin, v.ScaleMax = types.VectorKindScale, &lo, &hi
	}
	return []types.VectorSpec{v}
}

// runScaleArms runs req on the streaming arm (ungrouped) and the
// buffered arm (grouped), each over fresh records, and returns each
// arm's per-row scores under "s" (NaN = null) and its response.
func runScaleArms(t *testing.T, build func() *types.Request) (vals [3][]float64, resps [3]*types.Response) {
	t.Helper()
	schema := scaleSchema()
	// Arms: streaming ungrouped, streaming grouped, buffered grouped.
	for i, arm := range []struct {
		grouped, buffered bool
	}{{false, false}, {true, false}, {true, true}} {
		recs := scaleRecords(schema)
		req := build()
		req.Aggregations = append(req.Aggregations, &types.Aggregation{Type: types.AGG_SUM, Field: "s", Label: "sum_s"})
		if arm.grouped {
			req.Groups = []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}}
		}
		if arm.buffered {
			// A buffered-only attribute forces the buffered arm.
			req.Attributes = append(req.Attributes, &types.Attribute{Type: types.ATTR_PERCENTILE, Field: "q4", Label: "pct"})
		}
		p := NewProcessor(schema)
		r, err := p.Process(context.Background(), req, NewSliceIterator(recs))
		if err != nil {
			t.Fatalf("arm %+v: %v", arm, err)
		}
		want := map[bool]ProcessPath{false: PathStreaming, true: PathBuffered}[arm.buffered]
		if p.LastPath() != want {
			t.Fatalf("arm %+v ran %v, want %v", arm, p.LastPath(), want)
		}
		out := make([]float64, len(recs))
		for k, rec := range recs {
			if v, ok := rec.NumericValue("s"); ok {
				out[k] = v
			} else {
				out[k] = math.NaN()
			}
		}
		vals[i], resps[i] = out, r
	}
	return vals, resps
}

func scaleWarnings(resp *types.Response) []*types.ResponseWarning {
	var out []*types.ResponseWarning
	for _, w := range resp.Warnings {
		if w.Code == string(errors.PULSE_ATTR_SCALE_OUT_OF_RANGE) {
			out = append(out, w)
		}
	}
	return out
}

func sameScore(a, b float64) bool {
	if math.IsNaN(a) || math.IsNaN(b) {
		return math.IsNaN(a) && math.IsNaN(b)
	}
	return math.Abs(a-b) <= 1e-12*math.Max(1, math.Abs(b))
}

// TestScaleScore_RowSemantics pins every row rule — mean, prorated sum,
// min_valid null (never 0), reverse by member label on the kind "scale"
// vector's bounds or the params' range, out-of-range as missing with
// one counted warning — identically on the streaming and buffered arms.
func TestScaleScore_RowSemantics(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		vecs   []types.VectorSpec
		attr   types.Attribute
		want   []float64
		nOut   int64 // 0 = no warning
		nRows  int64
		lo, hi float64
	}{
		{name: "mean, min_valid every item, no range",
			attr: types.Attribute{Fields: []string{"q1", "q2", "q3", "q4"}},
			want: []float64{2.5, nan, nan, 3.75, 1}},
		{name: "prorated sum, min_valid 3",
			attr: types.Attribute{Fields: []string{"q*"}, Params: json.RawMessage(`{"stat": "sum", "min_valid": 3}`)},
			want: []float64{10, 8.0 / 3 * 4, nan, 15, 4}},
		{name: "reverse by label on kind scale bounds, out of range missing",
			vecs: scaleVector(true, 1, 5),
			attr: types.Attribute{Vector: "bat", Params: json.RawMessage(`{"reverse": ["b"], "min_valid": 1}`)},
			// r3: 9 is missing (never reversed); b=1 flips to 5.
			want: []float64{3, 8.0 / 3, 3, 10.0 / 3, 2},
			nOut: 1, nRows: 1, lo: 1, hi: 5},
		{name: "params range wins over vector bounds",
			vecs: scaleVector(true, 0, 10),
			attr: types.Attribute{Vector: "bat", Params: json.RawMessage(`{"scale_min": 1, "scale_max": 5, "min_valid": 1}`)},
			want: []float64{2.5, 8.0 / 3, 3, 2, 1},
			nOut: 1, nRows: 1, lo: 1, hi: 5},
		{name: "vector bounds hold every value",
			vecs: scaleVector(true, 0, 10),
			attr: types.Attribute{Vector: "bat"},
			want: []float64{2.5, nan, nan, 3.75, 1}},
		{name: "out of range drops a row below min_valid",
			vecs: scaleVector(true, 1, 5),
			attr: types.Attribute{Vector: "bat"},
			want: []float64{2.5, nan, nan, nan, 1},
			nOut: 1, nRows: 1, lo: 1, hi: 5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			vals, resps := runScaleArms(t, func() *types.Request {
				a := c.attr
				a.Type, a.Label = types.ATTR_SCALE_SCORE, "s"
				return &types.Request{Vectors: c.vecs, Attributes: []*types.Attribute{&a}}
			})
			for arm := range vals {
				for k, w := range c.want {
					if !sameScore(vals[arm][k], w) {
						t.Errorf("arm %d row %d = %v, want %v", arm, k, vals[arm][k], w)
					}
				}
				ws := scaleWarnings(resps[arm])
				if c.nOut == 0 {
					if len(ws) != 0 {
						t.Errorf("arm %d: unexpected warning %+v", arm, ws[0])
					}
					continue
				}
				if len(ws) != 1 {
					t.Fatalf("arm %d: %d out-of-range warnings, want 1 (%+v)", arm, len(ws), resps[arm].Warnings)
				}
				d := ws[0].Details
				if d["n_out_of_range"] != c.nOut || d["n_rows"] != c.nRows || d["scale_min"] != c.lo || d["scale_max"] != c.hi || d["attribute"] != "s" {
					t.Errorf("arm %d: details %v", arm, d)
				}
				if resps[arm].Warnings[0] != ws[0] {
					t.Errorf("arm %d: the attribute warning is not first: %+v", arm, resps[arm].Warnings)
				}
			}
		})
	}
}

// TestScaleScore_ReliabilityContrast: the same out-of-range battery is
// scored (as missing, with a warning) by ATTR_SCALE_SCORE and refused
// by MAT_RELIABILITY — both read the one vectors.ScaleKey.
func TestScaleScore_ReliabilityContrast(t *testing.T) {
	t.Parallel()

	schema := scaleSchema()
	vecs := scaleVector(true, 1, 5)
	recs := scaleRecords(schema)
	for i, r := range recs {
		r.SetMergePosition(0, 0, i)
	}
	p := NewProcessor(schema)
	_, err := p.Process(context.Background(), &types.Request{
		Vectors:  vecs,
		Matrices: []types.MatrixSpec{{Type: types.MAT_RELIABILITY, Name: "rel", Vector: "bat"}},
	}, NewSliceIterator(recs))
	var ce *errors.CodedError
	if !stderrors.As(err, &ce) || ce.Code != errors.PROCESSING_CONFIG {
		t.Fatalf("MAT_RELIABILITY over an out-of-range value: %v, want PROCESSING_CONFIG", err)
	}
	resp, err := NewProcessor(schema).Process(context.Background(), &types.Request{
		Vectors:      vecs,
		Attributes:   []*types.Attribute{{Type: types.ATTR_SCALE_SCORE, Vector: "bat", Label: "s", Params: json.RawMessage(`{"min_valid": 1}`)}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "s", Label: "n"}},
	}, NewSliceIterator(scaleRecords(schema)))
	if err != nil {
		t.Fatalf("ATTR_SCALE_SCORE refused: %v", err)
	}
	if len(scaleWarnings(resp)) != 1 {
		t.Errorf("want one out-of-range warning, got %+v", resp.Warnings)
	}
}

// TestScaleScore_FusedCrosstabMatchesBuffered: the fused crosstab arm
// drives the row-local attribute inline; its cells and its warning
// equal the buffered RunCrosstab's.
func TestScaleScore_FusedCrosstabMatchesBuffered(t *testing.T) {
	t.Parallel()

	schema := scaleSchema()
	build := func() *types.Request {
		return &types.Request{
			Vectors:    scaleVector(true, 1, 5),
			Attributes: []*types.Attribute{{Type: types.ATTR_SCALE_SCORE, Vector: "bat", Label: "s", Params: json.RawMessage(`{"min_valid": 2}`)}},
			Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
				Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "s"},
			},
		}
	}
	fused, err := NewProcessor(schema).RunCrosstabFused(context.Background(), build(), NewSliceIterator(scaleRecords(schema)))
	if err != nil {
		t.Fatal(err)
	}
	buffered, err := NewProcessor(schema).RunCrosstab(context.Background(), build(), scaleRecords(schema))
	if err != nil {
		t.Fatal(err)
	}
	fj, _ := json.Marshal(fused.Crosstab)
	bj, _ := json.Marshal(buffered.Crosstab)
	if string(fj) != string(bj) {
		t.Errorf("fused crosstab %s, buffered %s", fj, bj)
	}
	if len(scaleWarnings(fused)) != 1 || len(scaleWarnings(buffered)) != 1 {
		t.Errorf("warnings fused %+v, buffered %+v", fused.Warnings, buffered.Warnings)
	}
}

// TestScaleScore_FactoryRules: an unresolved vector is refused, a probe
// without members scores null, and canonical params decode.
func TestScaleScore_FactoryRules(t *testing.T) {
	t.Parallel()

	if _, err := newScaleScoreAttribute(&types.Attribute{Type: types.ATTR_SCALE_SCORE, Vector: "bat"}, scaleSchema()); err == nil {
		t.Error("an unresolved vector constructed")
	}
	probe, err := newScaleScoreAttribute(&types.Attribute{Type: types.ATTR_SCALE_SCORE}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := probe.(RowLocalAttribute).Row(scaleRecords(scaleSchema())[0], ""); !math.IsNaN(v) {
		t.Errorf("probe scored %v, want null", v)
	}
	if _, err := newScaleScoreAttribute(&types.Attribute{Type: types.ATTR_SCALE_SCORE, Fields: []string{"q1", "q2"},
		Params: json.RawMessage(`{"reverse": ["q1"]}`)}, scaleSchema()); err == nil {
		t.Error("reverse without a range constructed")
	}
}

// TestNeededFields_ScaleScoreMembers: projection keeps a scale score's
// members, named vector and inline fields alike — a dropped member
// would read as missing and silently move every score.
func TestNeededFields_ScaleScoreMembers(t *testing.T) {
	t.Parallel()

	schema := scaleSchema()
	for name, attr := range map[string]*types.Attribute{
		"vector": {Type: types.ATTR_SCALE_SCORE, Vector: "bat", Label: "s"},
		"fields": {Type: types.ATTR_SCALE_SCORE, Fields: []string{"q*"}, Label: "s"},
	} {
		req := &types.Request{
			Vectors:      scaleVector(true, 1, 5),
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "s"}},
			Attributes:   []*types.Attribute{attr},
		}
		fs := NeededFields(req, schema, nil)
		if fs.IsWide() {
			t.Errorf("%s: projection decodes wide", name)
			continue
		}
		for _, f := range []string{"q1", "q2", "q3", "q4"} {
			if !fs.Has(f) {
				t.Errorf("%s: projection omits %s", name, f)
			}
		}
		if fs.Has("g") {
			t.Errorf("%s: projection keeps the unread g", name)
		}
	}
}
