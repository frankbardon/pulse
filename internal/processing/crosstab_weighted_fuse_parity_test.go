package processing

import (
	"encoding/json"
	"fmt"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// Weighted crosstab fuse parity, one ACCUMULATOR CLASS at a time
// (weighting-descriptive E3-S2, .claude/reference/weighting.md
// "Crosstab"). The fused arm keeps a separate accumulator per class —
// cells, row / column / grand margins, the partial-depth
// (normalize_level) row and column denominators, the cross-axis
// (normalize_within) denominators, and the auxiliary
// margin_aggregations — and each is built off the stamped spec on its
// own construction site, so a class that forgot the weight would answer
// the UNWEIGHTED figure while every other class stays right. Each
// subtest isolates one class (the payload it alone produces) and
// asserts (a) fused equals buffered and (b) the weight moved that
// class's figure, so agreement on an unweighted answer cannot pass.
//
// Equality policy: AGG_COUNT and AGG_SUM are Σw-sums folded in row
// order by one statement shape on both arms — exact. AGG_AVERAGE's
// running and slice forms are separately inlined, so it compares within
// wfzRelTol (crosstab_fused_weighted_z_test.go's policy).

func wfpSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	dict := func(vals ...string) *encoding.Dictionary {
		d := encoding.NewDictionary()
		for _, v := range vals {
			if _, err := d.Add(v); err != nil {
				t.Fatalf("dict.Add(%q): %v", v, err)
			}
		}
		return d
	}
	return &encoding.Schema{Fields: []encoding.Field{
		{Name: "a", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict("a0", "a1", "a2")},
		{Name: "b", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict("b0", "b1")},
		{Name: "c", Type: encoding.FieldTypeCategoricalU8, Dictionary: dict("c0", "c1")},
		{Name: "x", Type: encoding.FieldTypeF64, Nullable: true},
		{Name: "w", Type: encoding.FieldTypeF64, Nullable: true},
	}}
}

// wfpRecords: fractional weights uncorrelated with x, plus a null, a
// zero and a negative weight sprinkled in (excluded on both arms) and
// null values (n_null on both arms).
func wfpRecords(schema *encoding.Schema) []*Record {
	var out []*Record
	for i := range 240 {
		vals := map[string]float64{
			"a": float64(i % 3), "b": float64((i / 3) % 2), "c": float64((i / 6) % 2),
			"x": float64((i*13)%17) - 4.5,
			"w": float64((i*7919)%97+1) / 13.0,
		}
		nulls := map[string]bool{}
		switch {
		case i%11 == 4:
			nulls["x"] = true
		case i%13 == 6:
			nulls["w"] = true
		case i%17 == 8:
			vals["w"] = 0
		case i%19 == 9:
			vals["w"] = -2
		}
		out = append(out, NewRecordWithNulls(schema, vals, nulls))
	}
	return out
}

// wfpRun runs req on one arm; weighted sets the request weight w.
func wfpRun(t *testing.T, req *types.Request, fused, weighted bool) *types.Response {
	t.Helper()
	schema := wfpSchema(t)
	var clone types.Request
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &clone); err != nil {
		t.Fatal(err)
	}
	if weighted {
		clone.Weight = &types.WeightSpec{Field: "w"}
	}
	if ok, why := CanFuseCrosstab(StampWeights(&clone, nil), schema, nil); !ok {
		t.Fatalf("fixture request is not fusable (%s); the parity would be vacuous", why)
	}
	p := NewProcessor(schema)
	var resp *types.Response
	if fused {
		resp, err = p.RunCrosstabFused(t.Context(), &clone, NewSliceIterator(wfpRecords(schema)))
	} else {
		resp, err = p.RunCrosstab(t.Context(), &clone, wfpRecords(schema))
	}
	if err != nil {
		t.Fatalf("fused=%v weighted=%v: %v", fused, weighted, err)
	}
	return resp
}

// wfpClass is one accumulator class: the spec shape that makes it
// emit, and the payload slice only it produces.
type wfpClass struct {
	name    string
	shape   func(s *types.CrosstabSpec)
	extract func(r *types.Response) any
}

func wfpClasses() []wfpClass {
	m := func(r *types.Response) *types.MatrixPayload { return r.Crosstab.Matrix }
	cc := func(r *types.Response) *types.CrosstabComponents { return r.Components.Crosstab }
	lvl := func(v int) *int { return &v }
	return []wfpClass{
		{"cells", func(*types.CrosstabSpec) {}, func(r *types.Response) any {
			return []any{m(r).Cells, cc(r).CellComponents}
		}},
		{"row_margin", func(s *types.CrosstabSpec) { s.Margins.Rows = true }, func(r *types.Response) any {
			return []any{m(r).RowMargins, cc(r).RowMarginComponents}
		}},
		{"column_margin", func(s *types.CrosstabSpec) { s.Margins.Columns = true }, func(r *types.Response) any {
			return []any{m(r).ColumnMargins, cc(r).ColumnMarginComponents}
		}},
		{"grand_margin", func(s *types.CrosstabSpec) { s.Margins.Grand = true }, func(r *types.Response) any {
			return []any{m(r).GrandTotal, cc(r).GrandTotalComponents}
		}},
		// normalize row at level 0 of a two-level row axis divides each
		// cell by its PARTIAL-depth row margin — the cells then carry
		// that accumulator's figure and nothing else's.
		{"partial_row_margin", func(s *types.CrosstabSpec) {
			s.Rows = append(s.Rows, &types.Group{Type: types.GROUP_CATEGORY, Field: "c"})
			s.Normalize = types.CrosstabNormalizeRow
			s.NormalizeLevel = lvl(0)
		}, func(r *types.Response) any { return m(r).Cells }},
		{"partial_column_margin", func(s *types.CrosstabSpec) {
			s.Columns = append(s.Columns, &types.Group{Type: types.GROUP_CATEGORY, Field: "c"})
			s.Normalize = types.CrosstabNormalizeColumn
			s.NormalizeLevel = lvl(0)
		}, func(r *types.Response) any { return m(r).Cells }},
		// normalize column within row level 0 divides each cell by the
		// CROSS-axis margin (row prefix × column).
		{"cross_margin", func(s *types.CrosstabSpec) {
			s.Rows = append(s.Rows, &types.Group{Type: types.GROUP_CATEGORY, Field: "c"})
			s.Normalize = types.CrosstabNormalizeColumn
			s.NormalizeWithin = lvl(0)
		}, func(r *types.Response) any { return m(r).Cells }},
		{"auxiliary", func(s *types.CrosstabSpec) {
			s.Margins = types.CrosstabMargins{Rows: true, Columns: true, Grand: true}
			s.MarginAggregations = []*types.Aggregation{
				{Type: types.AGG_SUM, Field: "x", Label: "aux_sum"},
				{Type: types.AGG_AVERAGE, Field: "x", Label: "aux_avg"},
				{Type: types.AGG_COUNT, Field: "x", Label: "base", Weight: types.NullSlotWeight()},
			}
		}, func(r *types.Response) any {
			c := cc(r)
			return []any{c.RowMarginAggregations, c.ColumnMarginAggregations, c.GrandTotalAggregations}
		}},
	}
}

// TestCrosstab_WeightedFusedMatchesBufferedPerClass: every fused
// accumulator class answers the buffered arm's weighted figure.
func TestCrosstab_WeightedFusedMatchesBufferedPerClass(t *testing.T) {
	for _, class := range wfpClasses() {
		for _, op := range []types.AggregationType{types.AGG_COUNT, types.AGG_SUM, types.AGG_AVERAGE} {
			t.Run(class.name+"/"+string(op), func(t *testing.T) {
				req := &types.Request{Crosstab: &types.CrosstabSpec{
					Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "a"}},
					Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "b"}},
					Cell:    &types.Aggregation{Type: op, Field: "x", Label: "cell"},
					Shape:   types.CrosstabShapeMatrix,
				}}
				class.shape(req.Crosstab)
				fused := wfpJSON(t, class.extract(wfpRun(t, req, true, true)))
				buffered := wfpJSON(t, class.extract(wfpRun(t, req, false, true)))
				unweighted := wfpJSON(t, class.extract(wfpRun(t, req, false, false)))
				if op == types.AGG_AVERAGE {
					wfpClose(t, class.name, fused, buffered)
				} else if fb, bb := wfpBytes(t, fused), wfpBytes(t, buffered); fb != bb {
					t.Errorf("%s: fused %s\n buffered %s", class.name, fb, bb)
				}
				if wfpBytes(t, buffered) == wfpBytes(t, unweighted) {
					t.Errorf("%s: the weight did not move the figure; the class is not under test", class.name)
				}
			})
		}
	}
}

func wfpJSON(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func wfpBytes(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// wfpClose: structure, keys and non-numbers exact; numbers within
// wfzRelTol relative.
func wfpClose(t *testing.T, where string, got, want any) {
	t.Helper()
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok || len(g) != len(w) {
			t.Errorf("%s: %v, want %v", where, got, want)
			return
		}
		for k := range w {
			wfpClose(t, where+"."+k, g[k], w[k])
		}
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			t.Errorf("%s: %v, want %v", where, got, want)
			return
		}
		for i := range w {
			wfpClose(t, fmt.Sprintf("%s[%d]", where, i), g[i], w[i])
		}
	case float64:
		g, ok := got.(float64)
		if !ok || (g != w && math.Abs(g-w) > wfzRelTol*math.Abs(w)) {
			t.Errorf("%s = %v, want %v", where, got, w)
		}
	default:
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%s = %v, want %v", where, got, want)
		}
	}
}
