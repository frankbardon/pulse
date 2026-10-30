package integration_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/internal/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// crosstab_cells_test.go pins the multi-measure crosstab
// (CrosstabSpec.Cells, U47 E2-S2 / #326): several measures as stub rows,
// a MEASURE position folded into the row axis.

const cellsCohortPath = "cells.pulse"

// writeCellsCohort lays out a survey-shaped cohort byte by byte: region
// and gender (categorical), tags (a nullable set — a null or empty set
// reaches no cell on a set axis), score (nullable f64) and wt (a
// probability weight). 60 rows, values from integer formulas so the
// fixture is identical on every architecture.
func writeCellsCohort(t *testing.T, fs afero.Fs) {
	t.Helper()
	dict := func(vals ...string) *encoding.Dictionary {
		d := encoding.NewDictionary()
		for _, v := range vals {
			if _, err := d.Add(v); err != nil {
				t.Fatal(err)
			}
		}
		return d
	}
	schema := &encoding.Schema{Fields: []encoding.Field{
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, Dictionary: dict("north", "south", "east")},
		{Name: "gender", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 1, Dictionary: dict("f", "m")},
		{Name: "tags", Type: encoding.FieldTypeSetU8, ByteOffset: 2, Nullable: true, Dictionary: dict("a", "b", "c")},
		{Name: "score", Type: encoding.FieldTypeF64, ByteOffset: 3, Nullable: true},
		{Name: "wt", Type: encoding.FieldTypeF64, ByteOffset: 11},
	}}
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatal(err)
	}
	stride := schema.RecordByteSize()
	for i := 0; i < 60; i++ {
		rec := make([]byte, stride)
		rec[0] = byte(i % 3)
		rec[1] = byte((i / 3) % 2)
		var bitmap byte
		switch i % 7 {
		case 0:
			bitmap |= 1 << 2 // null set
		case 3:
			rec[2] = 0 // empty set
		default:
			rec[2] = byte(1 + i%7) // a, b, c combinations
		}
		if i%11 == 5 {
			bitmap |= 1 << 3 // null score
		} else {
			binary.LittleEndian.PutUint64(rec[3:11], math.Float64bits(float64(i%9)+float64(i%4)/4))
		}
		binary.LittleEndian.PutUint64(rec[11:19], math.Float64bits(0.5+float64(i%5)/2))
		rec[stride-1] = bitmap
		buf.Write(rec)
	}
	if err := afero.WriteFile(fs, cellsCohortPath, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func cellsInstance(t *testing.T, fs afero.Fs, disableFusion bool) *pulse.Pulse {
	t.Helper()
	p, err := pulse.New(pulse.Options{FS: fs, DisableCrosstabFusion: disableFusion})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// cellsMeasures is the measure set most cases fold: a count, a mean and
// a sum — one summable, one mean-reducible, all fusable.
func cellsMeasures() []*types.Aggregation {
	return []*types.Aggregation{
		{Type: types.AGG_COUNT, Field: "score", Label: "n"},
		{Type: types.AGG_AVERAGE, Field: "score", Label: "mean"},
		{Type: types.AGG_SUM, Field: "score", Label: "total"},
	}
}

type cellsCase struct {
	name string
	spec func() *types.CrosstabSpec
	// weight is the request weight (nil: unweighted).
	weight *types.WeightSpec
}

func cellsCases() []cellsCase {
	region := func() []*types.Group { return []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}} }
	gender := func() []*types.Group { return []*types.Group{{Type: types.GROUP_CATEGORY, Field: "gender"}} }
	tags := func() []*types.Group { return []*types.Group{{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}} }
	all := types.CrosstabMargins{Rows: true, Columns: true, Grand: true}
	base := func(mut func(*types.CrosstabSpec)) func() *types.CrosstabSpec {
		return func() *types.CrosstabSpec {
			s := &types.CrosstabSpec{Rows: region(), Columns: gender(), Cells: cellsMeasures(), Margins: all}
			if mut != nil {
				mut(s)
			}
			return s
		}
	}
	zero := 0
	return []cellsCase{
		{name: "margins", spec: base(nil)},
		{name: "normalize_row", spec: base(func(s *types.CrosstabSpec) { s.Normalize = types.CrosstabNormalizeRow })},
		{name: "normalize_column", spec: base(func(s *types.CrosstabSpec) { s.Normalize = types.CrosstabNormalizeColumn })},
		{name: "normalize_total", spec: base(func(s *types.CrosstabSpec) { s.Normalize = types.CrosstabNormalizeTotal })},
		{name: "weighted", spec: base(nil), weight: &types.WeightSpec{Field: "wt"}},
		{name: "weighted_normalize_column", spec: base(func(s *types.CrosstabSpec) { s.Normalize = types.CrosstabNormalizeColumn }),
			weight: &types.WeightSpec{Field: "wt"}},
		{name: "set_columns", spec: base(func(s *types.CrosstabSpec) { s.Columns = tags() })},
		{name: "set_rows_normalize_column", spec: base(func(s *types.CrosstabSpec) {
			s.Rows, s.Columns, s.Normalize = tags(), region(), types.CrosstabNormalizeColumn
		})},
		{name: "nested_rows_normalize_within", spec: base(func(s *types.CrosstabSpec) {
			s.Rows = append(region(), gender()...)
			s.Columns = tags()
			s.Normalize, s.NormalizeWithin = types.CrosstabNormalizeColumn, &zero
		})},
		{name: "nested_rows_normalize_level", spec: base(func(s *types.CrosstabSpec) {
			s.Rows = append(region(), gender()...)
			s.Normalize, s.NormalizeLevel = types.CrosstabNormalizeRow, &zero
		})},
		{name: "total_column", spec: base(func(s *types.CrosstabSpec) { s.Columns = nil })},
		{name: "margin_aggregations", spec: base(func(s *types.CrosstabSpec) {
			s.MarginAggregations = []*types.Aggregation{{Type: types.AGG_COUNT, Field: "wt", Label: "base"}}
		})},
		{name: "long", spec: base(func(s *types.CrosstabSpec) { s.Shape = types.CrosstabShapeLong })},
		{name: "long_normalize_level", spec: base(func(s *types.CrosstabSpec) {
			s.Rows = append(region(), gender()...)
			s.Columns = tags()
			s.Shape, s.Normalize, s.NormalizeLevel = types.CrosstabShapeLong, types.CrosstabNormalizeRow, &zero
		})},
	}
}

func cellsRequest(spec *types.CrosstabSpec, weight *types.WeightSpec) *types.Request {
	return &types.Request{Cohort: &types.Cohort{Filename: cellsCohortPath}, Crosstab: spec, Weight: weight}
}

// TestCrosstabCells_EachMeasureIsItsSingleCellCrosstab pins the fold:
// every measure of a multi-measure crosstab is exactly the crosstab with
// Cell = that measure — cells, row margins, column margins, grand total
// and every Components counterpart — laid out over the MEASURE-folded
// rows (spec row r, measure m at r*M+m) with the per-measure column and
// grand figures on Measures. Run on both dispatch arms, across
// weighted, normalised, set-axis, nested and long-shape requests.
func TestCrosstabCells_EachMeasureIsItsSingleCellCrosstab(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	writeCellsCohort(t, fs)
	ctx := context.Background()
	for _, disable := range []bool{false, true} {
		p := cellsInstance(t, fs, disable)
		for _, c := range cellsCases() {
			t.Run(fmt.Sprintf("%s/disable_fusion_%v", c.name, disable), func(t *testing.T) {
				spec := c.spec()
				multi, err := p.Process(ctx, cellsRequest(spec, c.weight))
				if err != nil {
					t.Fatalf("multi-measure: %v", err)
				}
				labels := spec.MeasureLabels()
				M := len(labels)
				singles := make([]*types.Response, M)
				for m := range spec.Cells {
					single := c.spec()
					single.Cell, single.Cells = single.Cells[m], nil
					singles[m], err = p.Process(ctx, cellsRequest(single, c.weight))
					if err != nil {
						t.Fatalf("measure %d single: %v", m, err)
					}
				}
				if spec.ShapeOrDefault() == types.CrosstabShapeLong {
					var want []map[string]any
					for m, s := range singles {
						for _, row := range s.Data {
							cp := map[string]any{types.CrosstabMeasureField: labels[m]}
							for k, v := range row {
								cp[k] = v
							}
							want = append(want, cp)
						}
					}
					if got, w := mustJSON(t, multi.Data), mustJSON(t, want); got != w {
						t.Fatalf("long rows:\n got %s\nwant %s", got, w)
					}
					return
				}
				mx := multi.Crosstab.Matrix
				s0 := singles[0].Crosstab.Matrix
				wantHeader := types.AxisHeader{
					Fields: append(append([]string(nil), s0.RowHeader.Fields...), types.CrosstabMeasureField),
					Types:  append(append([]string(nil), s0.RowHeader.Types...), types.CrosstabMeasureType),
				}
				if !reflect.DeepEqual(mx.RowHeader, wantHeader) {
					t.Errorf("RowHeader = %+v, want %+v", mx.RowHeader, wantHeader)
				}
				if mx.CellLabel != "" || len(mx.ColumnMargins) != 0 || mx.GrandTotal.Present {
					t.Errorf("single-measure slots set on a multi-measure payload: label %q col %v grand %v",
						mx.CellLabel, mx.ColumnMargins, mx.GrandTotal)
				}
				if got, want := mustJSON(t, mx.ColumnKeys), mustJSON(t, s0.ColumnKeys); got != want {
					t.Errorf("ColumnKeys = %s, want %s", got, want)
				}
				rows := len(s0.RowKeys)
				if len(mx.RowKeys) != rows*M || len(mx.CellLabels) != rows*M {
					t.Fatalf("folded rows = %d keys / %d labels, want %d", len(mx.RowKeys), len(mx.CellLabels), rows*M)
				}
				mc := multi.Components.Crosstab
				for m, s := range singles {
					sm := s.Crosstab.Matrix
					sc := s.Components.Crosstab
					for r := 0; r < rows; r++ {
						at := r*M + m
						wantKey := append(append(types.AxisKey(nil), sm.RowKeys[r]...), labels[m])
						if got, want := mustJSON(t, mx.RowKeys[at]), mustJSON(t, wantKey); got != want {
							t.Errorf("RowKeys[%d] = %s, want %s", at, got, want)
						}
						if mx.CellLabels[at] != labels[m] {
							t.Errorf("CellLabels[%d] = %q, want %q", at, mx.CellLabels[at], labels[m])
						}
						if got, want := mustJSON(t, mx.Cells[at]), mustJSON(t, sm.Cells[r]); got != want {
							t.Errorf("measure %s row %d cells = %s, want %s", labels[m], r, got, want)
						}
						if got, want := mustJSON(t, mx.RowMargins[at]), mustJSON(t, sm.RowMargins[r]); got != want {
							t.Errorf("measure %s row %d margin = %s, want %s", labels[m], r, got, want)
						}
						if got, want := mustJSON(t, mc.CellComponents[at]), mustJSON(t, sc.CellComponents[r]); got != want {
							t.Errorf("measure %s row %d cell components = %s, want %s", labels[m], r, got, want)
						}
						if got, want := mustJSON(t, mc.RowMarginComponents[at]), mustJSON(t, sc.RowMarginComponents[r]); got != want {
							t.Errorf("measure %s row %d margin components = %s, want %s", labels[m], r, got, want)
						}
						if got, want := mc.RowMarginCounts[at], sc.RowMarginCounts[r]; got != want {
							t.Errorf("measure %s row %d margin count = %d, want %d", labels[m], r, got, want)
						}
						if sc.RowMarginAggregations != nil {
							if got, want := mustJSON(t, mc.RowMarginAggregations[at]), mustJSON(t, sc.RowMarginAggregations[r]); got != want {
								t.Errorf("measure %s row %d aux = %s, want %s", labels[m], r, got, want)
							}
						}
					}
					me := mx.Measures[m]
					if me.Label != labels[m] || mustJSON(t, me.ColumnMargins) != mustJSON(t, sm.ColumnMargins) ||
						mustJSON(t, me.GrandTotal) != mustJSON(t, sm.GrandTotal) {
						t.Errorf("Measures[%d] = %s, want label %q col %s grand %s", m, mustJSON(t, me), labels[m],
							mustJSON(t, sm.ColumnMargins), mustJSON(t, sm.GrandTotal))
					}
					want := types.CrosstabMeasureComponents{
						Label:                    labels[m],
						ColumnMarginCounts:       sc.ColumnMarginCounts,
						ColumnMarginComponents:   sc.ColumnMarginComponents,
						ColumnMarginAggregations: sc.ColumnMarginAggregations,
						GrandTotalCount:          sc.GrandTotalCount,
						GrandTotalComponents:     sc.GrandTotalComponents,
						GrandTotalAggregations:   sc.GrandTotalAggregations,
					}
					if got, w := mustJSON(t, mc.Measures[m]), mustJSON(t, want); got != w {
						t.Errorf("Components.Measures[%d] = %s, want %s", m, got, w)
					}
				}
				if mc.IncludedRecords != singles[0].Components.Crosstab.IncludedRecords ||
					len(mc.ColumnMarginCounts) != 0 || mc.GrandTotalCount != 0 {
					t.Errorf("components single-measure slots: included %d col %v grand %d",
						mc.IncludedRecords, mc.ColumnMarginCounts, mc.GrandTotalCount)
				}
			})
		}
	}
}

// TestCrosstabCells_FusedMatchesBuffered pins fused == buffered, byte for
// byte, for every multi-measure case — weighted, normalised (row, column,
// total, normalize_level, normalize_within), set axes, the implicit Total
// column, auxiliary margins, long shape and pairwise overlays — and that
// every case is one the fused arm actually takes.
func TestCrosstabCells_FusedMatchesBuffered(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	writeCellsCohort(t, fs)
	ctx := context.Background()
	fused := cellsInstance(t, fs, false)
	buffered := cellsInstance(t, fs, true)
	cases := cellsCases()
	cases = append(cases, cellsCase{name: "pairwise_prop_z", spec: func() *types.CrosstabSpec {
		s := cellsCases()[2].spec() // normalize_column
		s.Cells = []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "score", Label: "asked"},
			{Type: types.AGG_COUNT, Field: "wt", Label: "all"},
		}
		return s
	}, weight: &types.WeightSpec{Field: "wt"}})
	schema := cellsSchema(t, fs)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := func() *types.Request {
				r := cellsRequest(c.spec(), c.weight)
				if c.name == "pairwise_prop_z" {
					r.Overlays = []types.OverlaySpec{
						{Kind: types.OverlayKindPairwisePropZ, Scope: types.OverlayScopeColumn},
						{Kind: types.OverlayKindPairwisePropZ, Scope: types.OverlayScopeRow},
					}
				}
				return r
			}
			if ok, reason := processing.CanFuseCrosstab(req(), schema, nil); !ok {
				t.Fatalf("case is not fusable (%s): the parity would compare buffered with buffered", reason)
			}
			a, err := fused.Process(ctx, req())
			if err != nil {
				t.Fatalf("fused: %v", err)
			}
			b, err := buffered.Process(ctx, req())
			if err != nil {
				t.Fatalf("buffered: %v", err)
			}
			if got, want := mustJSON(t, a), mustJSON(t, b); got != want {
				t.Errorf("fused != buffered:\n fused    %s\n buffered %s", got, want)
			}
		})
	}
}

func cellsSchema(t *testing.T, fs afero.Fs) *encoding.Schema {
	t.Helper()
	data, err := afero.ReadFile(fs, cellsCohortPath)
	if err != nil {
		t.Fatal(err)
	}
	r := bytes.NewReader(data)
	v, err := encoding.ReadHeader(r)
	if err != nil {
		t.Fatal(err)
	}
	s, err := encoding.ReadSchema(r, v)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestCrosstabCells_PairwisePerMeasure pins that a pairwise overlay on a
// multi-measure crosstab is each measure's single-measure layer folded:
// COLUMN scope lays measure m's p-values on folded row r*M+m over the
// shared column pairs; ROW scope concatenates each measure's row pairs,
// labelled by the folded row key, so a pair never crosses measures.
func TestCrosstabCells_PairwisePerMeasure(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	writeCellsCohort(t, fs)
	ctx := context.Background()
	for _, disable := range []bool{false, true} {
		p := cellsInstance(t, fs, disable)
		for _, tc := range []struct {
			name  string
			kind  types.OverlayKind
			norm  types.CrosstabNormalize
			cells []*types.Aggregation
		}{
			{"prop_z", types.OverlayKindPairwisePropZ, types.CrosstabNormalizeColumn, []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "score", Label: "asked"},
				{Type: types.AGG_COUNT, Field: "wt", Label: "all"},
			}},
			{"welch_t", types.OverlayKindPairwiseWelchT, types.CrosstabNormalizeNone, []*types.Aggregation{
				{Type: types.AGG_WELFORD, Field: "score", Label: "score"},
				{Type: types.AGG_WELFORD, Field: "wt", Label: "wt"},
			}},
		} {
			for _, scope := range []types.OverlayScope{types.OverlayScopeColumn, types.OverlayScopeRow} {
				t.Run(fmt.Sprintf("%s/%s/disable_fusion_%v", tc.name, scope, disable), func(t *testing.T) {
					spec := func() *types.CrosstabSpec {
						return &types.CrosstabSpec{
							Rows:      []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
							Columns:   []*types.Group{{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}},
							Cells:     tc.cells,
							Normalize: tc.norm,
						}
					}
					ov := []types.OverlaySpec{{Kind: tc.kind, Scope: scope}}
					req := cellsRequest(spec(), nil)
					req.Overlays = ov
					multi, err := p.Process(ctx, req)
					if err != nil {
						t.Fatal(err)
					}
					got := multi.Overlays[0].Payload.Matrix
					M := len(tc.cells)
					rowsOut := 0
					count := 0
					for m := range tc.cells {
						single := spec()
						single.Cell, single.Cells = tc.cells[m], nil
						sreq := cellsRequest(single, nil)
						sreq.Overlays = ov
						sresp, err := p.Process(ctx, sreq)
						if err != nil {
							t.Fatal(err)
						}
						want := sresp.Overlays[0].Payload.Matrix
						count += *sresp.Overlays[0].Summary.Count
						if scope == types.OverlayScopeColumn {
							if mustJSON(t, got.ColumnKeys) != mustJSON(t, want.ColumnKeys) {
								t.Fatalf("pair keys = %s, want %s", mustJSON(t, got.ColumnKeys), mustJSON(t, want.ColumnKeys))
							}
							for r := range want.Cells {
								if g, w := mustJSON(t, got.Cells[r*M+m]), mustJSON(t, want.Cells[r]); g != w {
									t.Errorf("measure %d row %d p = %s, want %s", m, r, g, w)
								}
							}
							continue
						}
						for pi := range want.Cells {
							at := rowsOut + pi
							if g, w := mustJSON(t, got.Cells[at]), mustJSON(t, want.Cells[pi]); g != w {
								t.Errorf("measure %d pair %d p = %s, want %s", m, pi, g, w)
							}
							for _, leg := range got.RowKeys[at] {
								s, _ := leg.(string)
								if !bytes.HasSuffix([]byte(s), []byte(tc.cells[m].Label)) {
									t.Errorf("pair %d leg %q does not carry its measure %q", at, s, tc.cells[m].Label)
								}
							}
						}
						rowsOut += len(want.Cells)
					}
					if scope == types.OverlayScopeRow && len(got.Cells) != rowsOut {
						t.Errorf("row pairs = %d, want %d", len(got.Cells), rowsOut)
					}
					if *multi.Overlays[0].Summary.Count != count || count == 0 {
						t.Errorf("summary count = %d, want %d (non-zero)", *multi.Overlays[0].Summary.Count, count)
					}
				})
			}
		}
	}
}

// TestCrosstabCells_PredictMatchesRuntime pins predict ≡ runtime for the
// multi-measure crosstab on both dispatch arms: every shape predict
// accepts runs, and every refusal — the Cells faults, a measure label a
// margin aggregation reuses, a non-pairwise overlay, a pairwise kind one
// measure cannot feed, normalize over a map-valued measure — is raised
// under the same code by both.
func TestCrosstabCells_PredictMatchesRuntime(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	writeCellsCohort(t, fs)
	ctx := context.Background()
	xtab := func(mut func(*types.Request)) *types.Request {
		r := cellsRequest(&types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "gender"}},
			Cells:   cellsMeasures(),
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		}, nil)
		if mut != nil {
			mut(r)
		}
		return r
	}
	cases := []struct {
		name string
		req  *types.Request
		code errors.Code
	}{
		{"accepted", xtab(nil), ""},
		{"one measure", xtab(func(r *types.Request) { r.Crosstab.Cells = r.Crosstab.Cells[:1] }), ""},
		{"pairwise overlay", xtab(func(r *types.Request) {
			// cellsMeasures carries a mean: it names its p_source (E4-S6).
			r.Overlays = []types.OverlaySpec{{Kind: types.OverlayKindPairwisePropZ, Scope: types.OverlayScopeColumn,
				Params: json.RawMessage(`{"p_source":"cell_value_pct"}`)}}
		}), ""},
		{"cell and cells", xtab(func(r *types.Request) {
			r.Crosstab.Cell = &types.Aggregation{Type: types.AGG_COUNT, Field: "score"}
		}), errors.PULSE_CROSSTAB_CELLS_INVALID},
		{"null measure", xtab(func(r *types.Request) {
			r.Crosstab.Cells = append(r.Crosstab.Cells, nil)
		}), errors.PULSE_CROSSTAB_CELLS_INVALID},
		{"untyped measure", xtab(func(r *types.Request) {
			r.Crosstab.Cells = append(r.Crosstab.Cells, &types.Aggregation{Label: "x"})
		}), errors.PULSE_CROSSTAB_CELLS_INVALID},
		{"duplicate label", xtab(func(r *types.Request) {
			r.Crosstab.Cells[1].Label = "n"
		}), errors.PULSE_CROSSTAB_CELLS_DUPLICATE_LABEL},
		{"margin aggregation reuses a measure label", xtab(func(r *types.Request) {
			r.Crosstab.MarginAggregations = []*types.Aggregation{{Type: types.AGG_COUNT, Field: "wt", Label: "mean"}}
		}), errors.PULSE_CROSSTAB_MARGIN_AGG_DUPLICATE_LABEL},
		{"non-pairwise overlay", xtab(func(r *types.Request) {
			r.Overlays = []types.OverlaySpec{{Kind: types.OverlayKindShareOfRow, Scope: types.OverlayScopeCell,
				Ref: types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}}}}
		}), errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{"the same overlay on one cell", xtab(func(r *types.Request) {
			r.Crosstab.Cell, r.Crosstab.Cells = r.Crosstab.Cells[0], nil
			r.Overlays = []types.OverlaySpec{{Kind: types.OverlayKindShareOfRow, Scope: types.OverlayScopeCell,
				Ref: types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}}}}
		}), ""},
		{"welford kind over a non-welford measure", xtab(func(r *types.Request) {
			r.Crosstab.Cells = []*types.Aggregation{
				{Type: types.AGG_WELFORD, Field: "score", Label: "m"},
				{Type: types.AGG_COUNT, Field: "score", Label: "n"},
			}
			r.Overlays = []types.OverlaySpec{{Kind: types.OverlayKindPairwiseWelchT, Scope: types.OverlayScopeColumn}}
		}), errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE},
		{"normalize over a map-valued measure", xtab(func(r *types.Request) {
			r.Crosstab.Normalize = types.CrosstabNormalizeColumn
			r.Crosstab.Cells = append(r.Crosstab.Cells, &types.Aggregation{Type: types.AGG_SET_FREQUENCY, Field: "tags", Label: "f"})
		}), errors.PULSE_CROSSTAB_NORMALIZE_MAP_VALUED},
	}
	for _, disable := range []bool{false, true} {
		p := cellsInstance(t, fs, disable)
		for _, c := range cases {
			t.Run(fmt.Sprintf("%s/disable_fusion_%v", c.name, disable), func(t *testing.T) {
				env := predictEnvelope(t, p, fs, cellsCohortPath, c.req)
				_, perr := p.Process(ctx, c.req)
				if c.code == "" {
					if len(env.Errors) != 0 {
						t.Fatalf("predict refused: %q", envCodes(env))
					}
					if perr != nil {
						t.Fatalf("runtime refused what predict accepts: %v", perr)
					}
					return
				}
				if len(env.Errors) == 0 || env.Errors[0].Code != string(c.code) {
					t.Errorf("predict errors = %q, want first %s", envCodes(env), c.code)
				}
				if !errors.HasCode(perr, c.code) {
					t.Errorf("runtime err = %v, want %s", perr, c.code)
				}
			})
		}
	}
}

// TestCrosstabCells_PredictFusionAndStreamability pins that predict
// reports the dispatch the runtime takes for a multi-measure crosstab:
// fusable when every measure fuses, buffered when one measure is a
// recompute-margin aggregator, and the long / no-margin / no-normalize
// shape streamable like a single-measure one.
func TestCrosstabCells_PredictFusionAndStreamability(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	writeCellsCohort(t, fs)
	ctx := context.Background()
	p := cellsInstance(t, fs, false)
	schema := cellsSchema(t, fs)
	for _, tc := range []struct {
		name       string
		cells      []*types.Aggregation
		shape      types.CrosstabShape
		fusable    bool
		streamable bool
	}{
		{"all fusable", cellsMeasures(), types.CrosstabShapeMatrix, true, false},
		{"median measure", append(cellsMeasures(), &types.Aggregation{Type: types.AGG_MEDIAN, Field: "score", Label: "med"}),
			types.CrosstabShapeMatrix, false, false},
		{"long", cellsMeasures(), types.CrosstabShapeLong, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := cellsRequest(&types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "gender"}},
				Cells:   tc.cells,
				Shape:   tc.shape,
			}, nil)
			env := predictEnvelope(t, p, fs, cellsCohortPath, req)
			res := env.Data.(*descriptor.PredictResult)
			if res.CrosstabFusable == nil || *res.CrosstabFusable != tc.fusable {
				t.Errorf("predict CrosstabFusable = %v, want %v (reasons %q)", res.CrosstabFusable, tc.fusable, res.CrosstabFusionReasons)
			}
			if ok, _ := processing.CanFuseCrosstab(req, schema, nil); ok != tc.fusable {
				t.Errorf("runtime CanFuseCrosstab = %v, want %v", ok, tc.fusable)
			}
			if res.Streamable != tc.streamable {
				t.Errorf("predict Streamable = %v, want %v (reasons %q)", res.Streamable, tc.streamable, res.StreamableReasons)
			}
			if _, err := p.Process(ctx, req); err != nil {
				t.Fatalf("Process: %v", err)
			}
		})
	}
}

// TestCrosstabCells_SmartDefaultsPerEntry pins that each measure takes
// the aggregation default from its field's schema type — at runtime and
// in predict's DefaultsApplied, which names the measure's path.
func TestCrosstabCells_SmartDefaultsPerEntry(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	writeCellsCohort(t, fs)
	ctx := context.Background()
	p := cellsInstance(t, fs, false)
	req := func() *types.Request {
		return cellsRequest(&types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "gender"}},
			Cells: []*types.Aggregation{
				{Type: types.AGG_COUNT, Field: "score", Label: "n"},
				{Field: "score"},
			},
		}, nil)
	}
	resp, err := p.Process(ctx, req())
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Crosstab.Matrix.Measures[1].Label; got != "AGG_SUM_score" {
		t.Errorf("defaulted measure label = %q, want AGG_SUM_score", got)
	}
	pr, err := p.Predict(ctx, req())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range pr.DefaultsApplied {
		if reflect.DeepEqual(d.Path, []string{"Crosstab", "Cells", "1", "Type"}) && d.Type == string(types.AGG_SUM) {
			found = true
		}
	}
	if !found {
		t.Errorf("DefaultsApplied = %+v, want Crosstab.Cells.1.Type → AGG_SUM", pr.DefaultsApplied)
	}
	// The caller's request is untouched by predict's default pass.
	r := req()
	if _, err := p.Predict(ctx, r); err != nil {
		t.Fatal(err)
	}
	if r.Crosstab.Cells[1].Type != "" {
		t.Errorf("predict wrote the default into the caller's request: %q", r.Crosstab.Cells[1].Type)
	}
}

// TestCrosstabCells_HiddenCapabilityRefused pins the request-slot gate: an
// instance hiding capability:crosstab_cells refuses a set crosstab.cells
// as an unknown key, predict and runtime alike, and still runs Cell.
func TestCrosstabCells_HiddenCapabilityRefused(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	writeCellsCohort(t, fs)
	ctx := context.Background()
	p, err := pulse.New(pulse.Options{FS: fs, FeatureProfile: &pulse.FeatureProfile{Features: []string{
		"capability:process", "capability:crosstab", "AGG_COUNT", "AGG_AVERAGE", "AGG_SUM", "GROUP_CATEGORY",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	req := cellsRequest(&types.CrosstabSpec{
		Rows:  []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
		Cells: cellsMeasures(),
	}, nil)
	env := predictEnvelope(t, p, fs, cellsCohortPath, req)
	if len(env.Errors) == 0 || env.Errors[0].Code != string(errors.PULSE_REQUEST_UNKNOWN_FIELD) {
		t.Errorf("predict = %q, want PULSE_REQUEST_UNKNOWN_FIELD", envCodes(env))
	}
	if _, err := p.Process(ctx, req); !errors.HasCode(err, errors.PULSE_REQUEST_UNKNOWN_FIELD) {
		t.Errorf("runtime = %v, want PULSE_REQUEST_UNKNOWN_FIELD", err)
	}
	req.Crosstab.Cell, req.Crosstab.Cells = cellsMeasures()[0], nil
	if _, err := p.Process(ctx, req); err != nil {
		t.Errorf("single cell on the hidden instance: %v", err)
	}
}

// TestCrosstabCells_ComposeOverlayRefused pins that a Compose-host overlay
// reading a multi-measure crosstab slot is refused, PredictCompose and
// Compose under the same code.
func TestCrosstabCells_ComposeOverlayRefused(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	writeCellsCohort(t, fs)
	ctx := context.Background()
	p := cellsInstance(t, fs, false)
	slot := func(label string, multi bool) *types.Request {
		spec := &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "gender"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "score", Label: "n"},
		}
		if multi {
			spec.Cell, spec.Cells = nil, cellsMeasures()
		}
		r := cellsRequest(spec, nil)
		r.Label = label
		return r
	}
	creq := func(multi bool) *types.ComposedRequest {
		return &types.ComposedRequest{
			Requests: []*types.Request{slot("a", false), slot("b", multi)},
			Overlays: []types.ComposeOverlaySpec{{Kind: types.OverlayKindIndexVsRef, Reference: "a", Targets: []string{"b"}}},
		}
	}
	if _, err := p.Compose(ctx, creq(false)); err != nil {
		t.Fatalf("control (single-measure slots): %v", err)
	}
	_, err := p.Compose(ctx, creq(true))
	if !errors.HasCode(err, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
		t.Errorf("runtime = %v, want PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE", err)
	}
	env, perr := p.PredictCompose(ctx, creq(true))
	if perr != nil {
		t.Fatal(perr)
	}
	found := false
	for _, e := range env.Errors {
		found = found || e.Code == string(errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE)
	}
	if !found {
		t.Errorf("PredictCompose = %q, want PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE", envCodes(env))
	}
}

// TestCrosstabCells_PairwiseSkipWarningsFold pins the folded layer's skip
// warnings: one per reason across every measure, in first-seen order,
// counts summed, and a COLUMN-scope sample index naming the FOLDED row
// (measure m of spec row r at r*M+m) so it addresses the payload the
// layer sits on.
func TestCrosstabCells_PairwiseSkipWarningsFold(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	writeCellsCohort(t, fs)
	ctx := context.Background()
	p := cellsInstance(t, fs, false)
	cells := []*types.Aggregation{
		{Type: types.AGG_WELFORD, Field: "wt", Label: "w"},
		{Type: types.AGG_WELFORD, Field: "score", Label: "s"},
	}
	spec := func() *types.CrosstabSpec {
		// Sparse on purpose: gender × one-point score buckets against the
		// tags set leaves cells with n < 2 and cells with no record.
		return &types.CrosstabSpec{
			Rows: []*types.Group{
				{Type: types.GROUP_CATEGORY, Field: "gender"},
				{Type: types.GROUP_RANGE, Field: "score", Interval: 1},
			},
			Columns: []*types.Group{{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}},
			Cells:   cells,
		}
	}
	ov := []types.OverlaySpec{{Kind: types.OverlayKindPairwiseWelchT, Scope: types.OverlayScopeColumn}}
	req := cellsRequest(spec(), nil)
	req.Overlays = ov
	multi, err := p.Process(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	M := len(cells)
	type agg struct {
		skipped int
		sample  int
	}
	var order []string
	want := map[string]*agg{}
	for m := range cells {
		single := spec()
		single.Cell, single.Cells = cells[m], nil
		sreq := cellsRequest(single, nil)
		sreq.Overlays = ov
		sresp, err := p.Process(ctx, sreq)
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range sresp.Overlays[0].Warnings {
			reason := w.Details["reason"].(string)
			if _, ok := want[reason]; !ok {
				order = append(order, reason)
				want[reason] = &agg{sample: w.Details["sample_opposite_idx"].(int)*M + m}
			}
			want[reason].skipped += w.Details["skipped"].(int)
		}
	}
	if len(order) == 0 {
		t.Fatal("vacuous: no single-measure layer skipped a pair")
	}
	got := multi.Overlays[0].Warnings
	if len(got) != len(order) {
		t.Fatalf("warnings = %+v, want reasons %v", got, order)
	}
	for i, reason := range order {
		d := got[i].Details
		if d["reason"] != reason || d["skipped"] != want[reason].skipped || d["sample_opposite_idx"] != want[reason].sample {
			t.Errorf("warning %d = %v, want reason %s skipped %d sample %d", i, d, reason, want[reason].skipped, want[reason].sample)
		}
	}
}
