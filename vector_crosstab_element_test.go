package pulse_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	perr "github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Crosstab vector element (U30 E4-S4): an AGG_VEC_* crosstab cell or
// margin aggregation runs only when params.element selects ONE element
// — the slot then reads that member alone, so the cell is a scalar
// (the element's mean / sum), its floor counts the rows that answered
// the member, and an auxiliary margin admits only the records whose
// selected member reached a cell. Without element the array-valued
// refusal stands.

// vxElementRequest is the element crosstab over the vexpr battery: rows
// by qty, columns by b1 (nullable, so some records reach a row margin
// but no cell), cell = element b3 of AGG_VEC_MEAN over b1..b4, one
// auxiliary = element b2 of AGG_VEC_SUM.
func vxElementRequest(cohort string, normalize types.CrosstabNormalize) *types.Request {
	return &types.Request{Cohort: &types.Cohort{Filename: cohort}, Crosstab: &types.CrosstabSpec{
		Rows:      []*types.Group{{Type: types.GROUP_CATEGORY, Field: "qty"}},
		Columns:   []*types.Group{{Type: types.GROUP_CATEGORY, Field: "b1"}},
		Cell:      &types.Aggregation{Type: types.AGG_VEC_MEAN, Fields: []string{"b*"}, Label: "m", Params: json.RawMessage(`{"element":"b3"}`)},
		Margins:   types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		Normalize: normalize,
		MarginAggregations: []*types.Aggregation{
			{Type: types.AGG_VEC_SUM, Fields: []string{"b*"}, Label: "s", Params: json.RawMessage(`{"element":"b2"}`)},
		},
	}}
}

// vxScalarTwin is the same crosstab over the scalar columns: AGG_AVERAGE
// of b3, AGG_SUM of b2.
func vxScalarTwin(cohort string, normalize types.CrosstabNormalize) *types.Request {
	req := vxElementRequest(cohort, normalize)
	req.Crosstab.Cell = &types.Aggregation{Type: types.AGG_AVERAGE, Field: "b3", Label: "m"}
	req.Crosstab.MarginAggregations = []*types.Aggregation{{Type: types.AGG_SUM, Field: "b2", Label: "s"}}
	return req
}

func vxCellFloat(t *testing.T, c types.MatrixCell) float64 {
	t.Helper()
	if !c.Present {
		return math.NaN()
	}
	switch v := c.Value.(type) {
	case float64:
		return v
	case nil:
		return math.NaN()
	}
	t.Fatalf("cell value %v (%T) is not a scalar", c.Value, c.Value)
	return 0
}

// vxCells flattens a crosstab's cells and margins to numbers.
func vxCells(t *testing.T, r *types.Response) []float64 {
	t.Helper()
	if r.Crosstab == nil || r.Crosstab.Matrix == nil {
		t.Fatal("no crosstab matrix")
	}
	m := r.Crosstab.Matrix
	var out []float64
	for _, row := range m.Cells {
		for _, c := range row {
			out = append(out, vxCellFloat(t, c))
		}
	}
	for _, c := range m.RowMargins {
		out = append(out, vxCellFloat(t, c))
	}
	for _, c := range m.ColumnMargins {
		out = append(out, vxCellFloat(t, c))
	}
	return append(out, vxCellFloat(t, m.GrandTotal))
}

// vxFloors is every cell / margin floor {n, n_null, sum_weights}.
func vxFloors(r *types.Response) [][3]any {
	cc := r.Components.Crosstab
	var out [][3]any
	add := func(m map[string]any) {
		if m == nil {
			out = append(out, [3]any{nil, nil, nil})
			return
		}
		out = append(out, [3]any{m["n"], m["n_null"], m["sum_weights"]})
	}
	for _, row := range cc.CellComponents {
		for _, m := range row {
			add(m)
		}
	}
	for _, m := range cc.RowMarginComponents {
		add(m)
	}
	for _, m := range cc.ColumnMarginComponents {
		add(m)
	}
	add(cc.GrandTotalComponents)
	return out
}

// vxAux is every auxiliary figure "s": value and floor.
func vxAux(t *testing.T, r *types.Response) []any {
	t.Helper()
	cc := r.Components.Crosstab
	var out []any
	add := func(f types.MarginAggregationFigure) {
		v := any(nil)
		if f.Present {
			v = f.Value
		}
		out = append(out, []any{v, f.Present, f.Components["n"], f.Components["n_null"], f.Components["sum_weights"]})
	}
	for _, m := range cc.RowMarginAggregations {
		add(m["s"])
	}
	for _, m := range cc.ColumnMarginAggregations {
		add(m["s"])
	}
	add(cc.GrandTotalAggregations["s"])
	return out
}

func vxSame(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !sameFloat(a[i], b[i]) {
			return false
		}
	}
	return true
}

// TestVecAggCrosstab_ElementCell: the element cell equals the scalar
// twin (AGG_AVERAGE of the member) cell for cell, margin for margin, on
// both crosstab arms and every decode split, under every normalize
// mode; the floor and the auxiliary (admission) match the twin's; the
// cell components are the selected element's; predict accepts it and
// reports the fused arm exactly as the twin does.
func TestVecAggCrosstab_ElementCell(t *testing.T) {
	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	writeVexprCohort(t, fsys, "v.pulse", 0, 90)
	data, err := afero.ReadFile(fsys, "v.pulse")
	if err != nil {
		t.Fatal(err)
	}
	newP := func(o pulse.Options) *pulse.Pulse {
		o.FS = fsys
		p, err := pulse.New(o)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	base := newP(pulse.Options{})
	arms := map[string]*pulse.Pulse{
		"fused":             base,
		"buffered":          newP(pulse.Options{DisableCrosstabFusion: true}),
		"fused serial":      newP(pulse.Options{DecodeWorkers: 1}),
		"buffered parallel": newP(pulse.Options{DisableCrosstabFusion: true, DecodeWorkers: 4}),
	}
	type run struct {
		norm     types.CrosstabNormalize
		weighted bool
	}
	var runs []run
	for _, norm := range []types.CrosstabNormalize{types.CrosstabNormalizeNone, types.CrosstabNormalizeRow, types.CrosstabNormalizeColumn, types.CrosstabNormalizeTotal} {
		runs = append(runs, run{norm, false}, run{norm, true})
	}
	for _, rn := range runs {
		norm := rn.norm
		weigh := func(r *types.Request) *types.Request {
			if rn.weighted {
				r.Weight = &types.WeightSpec{Field: "score"}
			}
			return r
		}
		vxElementRequest := func(c string, n types.CrosstabNormalize) *types.Request { return weigh(vxElementRequest(c, n)) }
		vxScalarTwin := func(c string, n types.CrosstabNormalize) *types.Request { return weigh(vxScalarTwin(c, n)) }
		t.Run(fmt.Sprintf("%s/weighted=%v", norm, rn.weighted), func(t *testing.T) {
			// predict ≡ runtime: valid, and fusable exactly as the twin.
			pe, err := base.Predict(ctx, vxElementRequest("v.pulse", norm))
			if err != nil || !pe.Valid {
				t.Fatalf("predict refused the element crosstab: %v %+v", err, pe)
			}
			pt, err := base.Predict(ctx, vxScalarTwin("v.pulse", norm))
			if err != nil {
				t.Fatal(err)
			}
			if pe.CrosstabFusable == nil || pt.CrosstabFusable == nil || *pe.CrosstabFusable != *pt.CrosstabFusable || !*pe.CrosstabFusable {
				t.Fatalf("CrosstabFusable element %v (reasons %v), twin %v (reasons %v) — want both fused",
					pe.CrosstabFusable != nil && *pe.CrosstabFusable, pe.CrosstabFusionReasons,
					pt.CrosstabFusable != nil && *pt.CrosstabFusable, pt.CrosstabFusionReasons)
			}
			env, err := base.PredictBytes(ctx, data, vxElementRequest("v.pulse", norm))
			if err != nil || len(env.Errors) != 0 {
				t.Fatalf("PredictBytes = %v %+v", err, env.Errors)
			}
			for _, w := range env.Warnings {
				if w.Code == string(perr.PULSE_CROSSTAB_NORMALIZE_UNSATISFIABLE) {
					t.Errorf("an element cell margins like its scalar twin; got %s", w.Code)
				}
			}

			twin, err := base.Process(ctx, vxScalarTwin("v.pulse", norm))
			if err != nil {
				t.Fatal(err)
			}
			want, wantFloors, wantAux := vxCells(t, twin), vxFloors(twin), vxAux(t, twin)
			var ref []float64
			for name, p := range arms {
				resp, err := p.Process(ctx, vxElementRequest("v.pulse", norm))
				if err != nil {
					t.Fatalf("%s: Process: %v", name, err)
				}
				got := vxCells(t, resp)
				nanCells := 0
				for i := range got {
					if wantFloors[i][0] == 0 {
						// No row answered the member: the element mean is
						// undefined (null), never 0. (The twin's
						// AGG_AVERAGE still reads 0 there — E4-S5.)
						if !math.IsNaN(got[i]) {
							t.Errorf("%s: figure %d over no answered row = %v, want NaN", name, i, got[i])
						}
						nanCells++
						continue
					}
					if !sameFloat(got[i], want[i]) && !vexprClose(got[i], want[i]) {
						t.Fatalf("%s: figure %d = %v, scalar twin %v", name, i, got[i], want[i])
					}
				}
				if nanCells == 0 || nanCells == len(got) {
					t.Fatalf("%s: fixture drift — %d of %d figures undefined", name, nanCells, len(got))
				}
				if ref == nil {
					ref = got
				} else if !vxSame(got, ref) {
					t.Errorf("%s: figures differ bitwise from another arm", name)
				}
				if f := vxFloors(resp); !reflect.DeepEqual(f, wantFloors) {
					t.Errorf("%s: floors %v, twin %v", name, f, wantFloors)
				}
				if a := vxAux(t, resp); !reflect.DeepEqual(a, wantAux) {
					t.Errorf("%s: auxiliary %v, twin %v", name, a, wantAux)
				}
				// The cell components are the selected element's.
				g := resp.Components.Crosstab.GrandTotalComponents
				if !reflect.DeepEqual(g["labels"], []string{"b3"}) {
					t.Errorf("%s: grand labels = %v, want [b3]", name, g["labels"])
				}
				if en, ok := g["element_n"].([]int); !ok || len(en) != 1 || en[0] != g["n"] {
					t.Errorf("%s: grand element_n = %v, want [n=%v]", name, g["element_n"], g["n"])
				}
			}
		})
	}
}

// TestVecAggCrosstab_ElementRefusalParity: each element refusal is
// raised by Process and predict alike, with the same code and reason;
// without element the array-valued refusal stands.
func TestVecAggCrosstab_ElementRefusalParity(t *testing.T) {
	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	writeVexprCohort(t, fsys, "v.pulse", 0, 40)
	data, err := afero.ReadFile(fsys, "v.pulse")
	if err != nil {
		t.Fatal(err)
	}
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	cell := func(params string) func() *types.Request {
		return func() *types.Request {
			r := vxElementRequest("v.pulse", "")
			r.Crosstab.MarginAggregations = nil
			r.Crosstab.Cell.Params = json.RawMessage(params)
			return r
		}
	}
	cases := []struct {
		name   string
		req    func() *types.Request
		code   perr.Code
		reason string
	}{
		{"no element", cell(`{}`), perr.PROCESSING_CONFIG, "array_valued"},
		{"unknown element", cell(`{"element":"b9"}`), perr.SERVICE_VALIDATION, "element_unknown"},
		{"element with expand", cell(`{"element":"b3","expand":true}`), perr.SERVICE_VALIDATION, "element_with_expand"},
		{"ambiguous element", func() *types.Request {
			r := cell(`{"element":"x","labels":["x","x","y","z"]}`)()
			return r
		}, perr.SERVICE_VALIDATION, "element_ambiguous"},
		{"margin without element", func() *types.Request {
			r := vxElementRequest("v.pulse", "")
			r.Crosstab.MarginAggregations[0].Params = nil
			return r
		}, perr.PROCESSING_CONFIG, "array_valued"},
		{"element outside a crosstab", func() *types.Request {
			return &types.Request{Cohort: &types.Cohort{Filename: "v.pulse"},
				Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "qty"}},
				Aggregations: []*types.Aggregation{{Type: types.AGG_VEC_MEAN, Fields: []string{"b*"}, Label: "m",
					Params: json.RawMessage(`{"element":"b3"}`)}}}
		}, perr.SERVICE_VALIDATION, "element_outside_crosstab"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := p.Process(ctx, c.req())
			ce := codeOf(t, err)
			if ce.Code != c.code || ce.Details["reason"] != c.reason {
				t.Fatalf("Process = %s %v, want %s %s", ce.Code, ce.Details, c.code, c.reason)
			}
			env, err := p.PredictBytes(ctx, data, c.req())
			if err != nil {
				t.Fatal(err)
			}
			if !vaggHasReason(env, c.code, c.reason) {
				t.Fatalf("predict = %+v, want %s %s", env.Errors, c.code, c.reason)
			}
		})
	}
}

// TestVecAggCrosstab_ElementBitExactAcrossWorkers: an element cell and
// auxiliary over non-dyadic members spanning several merge blocks
// answer the same bits on both crosstab arms, serially and under every
// DecodeWorkers count, and over a shard archive of the same rows (shard
// edges inside merge blocks, a 1-row and an empty shard) under every
// ShardWorkers × DecodeWorkers count — weighted and unweighted.
func TestVecAggCrosstab_ElementBitExactAcrossWorkers(t *testing.T) {
	const rows = 2*4096 + 1234
	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	writeVaggBitsCohort(t, fsys, "single.pulse", 0, rows)
	bounds := []int{0, 1, 2, 4097, 4097, 6000, rows}
	var shards []string
	for i := 0; i+1 < len(bounds); i++ {
		path := fmt.Sprintf("s%d.pulse", i)
		writeVaggBitsCohort(t, fsys, path, bounds[i], bounds[i+1])
		shards = append(shards, path)
	}
	builder, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := builder.CreateShardArchive(ctx, "archive.pulse", shards); err != nil {
		t.Fatal(err)
	}
	req := func(cohort string, weighted bool) *types.Request {
		r := &types.Request{Cohort: &types.Cohort{Filename: cohort}, Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "qty"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "score"}},
			Cell:    &types.Aggregation{Type: types.AGG_VEC_MEAN, Fields: []string{"b*"}, Label: "m", Params: json.RawMessage(`{"element":"b2"}`)},
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			MarginAggregations: []*types.Aggregation{
				{Type: types.AGG_VEC_SUM, Fields: []string{"b*"}, Label: "s", Params: json.RawMessage(`{"element":"b3"}`)},
			},
		}}
		if weighted {
			r.Weight = &types.WeightSpec{Field: "score"}
		}
		return r
	}
	bits := func(p *pulse.Pulse, cohort string, weighted bool) []uint64 {
		t.Helper()
		resp, err := p.Process(ctx, req(cohort, weighted))
		if err != nil {
			t.Fatalf("Process: %v", err)
		}
		var out []uint64
		for _, x := range vxCells(t, resp) {
			out = append(out, math.Float64bits(x))
		}
		for _, a := range vxAux(t, resp) {
			if v, ok := a.([]any)[0].(float64); ok {
				out = append(out, math.Float64bits(v))
			}
		}
		return out
	}
	for _, weighted := range []bool{false, true} {
		t.Run(fmt.Sprintf("weighted=%v", weighted), func(t *testing.T) {
			ref := bits(builder, "single.pulse", weighted)
			for _, fuse := range []bool{true, false} {
				for _, dw := range []int{1, 3} {
					p, err := pulse.New(pulse.Options{FS: fsys, DisableCrosstabFusion: !fuse, DecodeWorkers: dw})
					if err != nil {
						t.Fatal(err)
					}
					if got := bits(p, "single.pulse", weighted); !reflect.DeepEqual(got, ref) {
						t.Errorf("single fused=%v DecodeWorkers=%d: bits differ", fuse, dw)
					}
					for _, sw := range []int{1, 2, 4} {
						p, err := pulse.New(pulse.Options{FS: fsys, DisableCrosstabFusion: !fuse, DecodeWorkers: dw, ShardWorkers: sw})
						if err != nil {
							t.Fatal(err)
						}
						if got := bits(p, "archive.pulse", weighted); !reflect.DeepEqual(got, ref) {
							t.Errorf("archive fused=%v ShardWorkers=%d DecodeWorkers=%d: bits differ from the single-file twin", fuse, sw, dw)
						}
					}
				}
			}
		})
	}
}
