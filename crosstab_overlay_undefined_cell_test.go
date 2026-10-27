package pulse_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"testing"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// An undefined crosstab cell is MISSING to every crosstab-host overlay
// (matrix-extensions-hardening E4-S6). AGG_AVERAGE of v over the cell
// (r1 0, r2 1, c 1) is NaN — its rows exist, none answered v — and the
// request host's overlays read it as they read an absent cell: the
// overlay cell is absent, no warning names it, it is out of the summary
// count, and it never reaches a denominator (the Level / Within bucket
// sum) or a slice SD (ZSCORE_VS_MARGIN). Before, the cell scored a
// spurious non-finite PULSE_OVERLAY_REF_ZERO, and the bucket sum / the
// row SD it joined turned every sibling cell non-finite too. Identical
// on the fused and the buffered crosstab arm.

func undefCellSchema() *encoding.Schema {
	fields := []encoding.Field{
		{Name: "r1", Type: encoding.FieldTypeU8},
		{Name: "r2", Type: encoding.FieldTypeU8},
		{Name: "c", Type: encoding.FieldTypeU8},
		{Name: "v", Type: encoding.FieldTypeF64, Nullable: true},
	}
	off := 0
	for i := range fields {
		fields[i].ByteOffset = off
		fields[i].CsvColumnIdx = i
		off += fields[i].Type.ByteSize()
	}
	return &encoding.Schema{Fields: fields}
}

// writeUndefCellCohort writes three rows per (r1, r2, c) cell; no row of
// the cell at nan answers v.
func writeUndefCellCohort(t *testing.T, fsys afero.Fs, path string, nan [3]int) {
	t.Helper()
	s := undefCellSchema()
	var buf []byte
	w := &byteSink{b: &buf}
	if err := encoding.WriteHeader(w); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(w, s); err != nil {
		t.Fatal(err)
	}
	stride := s.RecordByteSize()
	bm := s.BitmapByteSize()
	for r1 := 0; r1 < 2; r1++ {
		for r2 := 0; r2 < 2; r2++ {
			for c := 0; c < 3; c++ {
				for k := 0; k < 3; k++ {
					rec := make([]byte, stride)
					rec[s.Fields[0].ByteOffset] = byte(r1)
					rec[s.Fields[1].ByteOffset] = byte(r2)
					rec[s.Fields[2].ByteOffset] = byte(c)
					if [3]int{r1, r2, c} == nan {
						encoding.BitmapSetNull(rec[stride-bm:], 3)
					} else {
						v := math.Float64bits(1 + float64(r1*7+r2*3+c*c*2) + 0.5*float64(k))
						for i := 0; i < 8; i++ {
							rec[s.Fields[3].ByteOffset+i] = byte(v >> (8 * i))
						}
					}
					buf = append(buf, rec...)
				}
			}
		}
	}
	if err := afero.WriteFile(fsys, path, buf, 0o644); err != nil {
		t.Fatal(err)
	}
}

func undefCellRequest(overlay types.OverlaySpec) *types.Request {
	req := undefCellSlot("", "u.pulse")
	req.Overlays = []types.OverlaySpec{overlay}
	return req
}

func undefCellSlot(label, cohort string) *types.Request {
	return &types.Request{Label: label, Cohort: &types.Cohort{Filename: cohort}, Crosstab: &types.CrosstabSpec{
		Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "r1"}, {Type: types.GROUP_CATEGORY, Field: "r2"}},
		Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "c"}},
		Cell:    &types.Aggregation{Type: types.AGG_AVERAGE, Field: "v", Label: "cell"},
		Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		Shape:   types.CrosstabShapeMatrix,
	}}
}

// TestCrosstabOverlays_UndefinedCellIsMissing: one rule per crosstab
// kind that reads cell values, on both crosstab arms.
func TestCrosstabOverlays_UndefinedCellIsMissing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	writeUndefCellCohort(t, fsys, "u.pulse", [3]int{0, 1, 1})
	newP := func(o pulse.Options) *pulse.Pulse {
		o.FS = fsys
		p, err := pulse.New(o)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	fused, buffered := newP(pulse.Options{}), newP(pulse.Options{DisableCrosstabFusion: true})
	margin := func(a types.MarginAxis) types.OverlayRef {
		return types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: a}}
	}
	cell := func(kind types.OverlayKind, axis types.MarginAxis, level int) types.OverlaySpec {
		return types.OverlaySpec{Name: "o", Kind: kind, Scope: types.OverlayScopeCell, Ref: margin(axis), Level: level}
	}
	formula, _ := json.Marshal(map[string]any{"formula": "cell - sd_row"})
	for _, c := range []struct {
		name string
		spec types.OverlaySpec
	}{
		{"SHARE_OF_ROW", cell(types.OverlayKindShareOfRow, types.MarginAxisRow, 0)},
		// Level 1: the denominator is the bucket sum over the rows that
		// share r1, the undefined cell among them.
		{"SHARE_OF_ROW/level", cell(types.OverlayKindShareOfRow, types.MarginAxisRow, 1)},
		{"SHARE_OF_COL", cell(types.OverlayKindShareOfCol, types.MarginAxisColumn, 0)},
		{"SHARE_OF_TOTAL", cell(types.OverlayKindShareOfTotal, types.MarginAxisGrand, 0)},
		{"INDEX_VS_MARGIN", cell(types.OverlayKindIndexVsMargin, types.MarginAxisRow, 0)},
		{"INDEX_VS_MARGIN/level", cell(types.OverlayKindIndexVsMargin, types.MarginAxisRow, 1)},
		{"DELTA_VS_MARGIN", cell(types.OverlayKindDeltaVsMargin, types.MarginAxisColumn, 0)},
		// The row SD is taken over the row's defined cells only.
		{"ZSCORE_VS_MARGIN/row", cell(types.OverlayKindZScoreVsMargin, types.MarginAxisRow, 0)},
		{"ZSCORE_VS_MARGIN/grand", cell(types.OverlayKindZScoreVsMargin, types.MarginAxisGrand, 0)},
		{"FORMULA", types.OverlaySpec{Name: "o", Kind: types.OverlayKindFormula, Scope: types.OverlayScopeCell, Params: formula}},
	} {
		t.Run(c.name, func(t *testing.T) {
			// The default instance takes the fused arm (predict ≡ runtime
			// dispatch), so the two arms below are distinct.
			pr, err := fused.Predict(ctx, undefCellRequest(c.spec))
			if err != nil || pr.CrosstabFusable == nil || !*pr.CrosstabFusable {
				t.Fatalf("predict: fusable %v (%v), want the fused arm", pr.CrosstabFusable, err)
			}
			var layers []string
			for arm, p := range map[string]*pulse.Pulse{"fused": fused, "buffered": buffered} {
				resp, err := p.Process(ctx, undefCellRequest(c.spec))
				if err != nil {
					t.Fatalf("%s: %v", arm, err)
				}
				host := resp.Crosstab.Matrix
				layer := resp.Overlays[0]
				if len(resp.Warnings) != 0 || len(layer.Warnings) != 0 {
					t.Errorf("%s: warnings %+v / %+v, want none", arm, resp.Warnings, layer.Warnings)
				}
				mx := layer.Payload.Matrix
				present := 0
				for i, rk := range host.RowKeys {
					for j, ck := range host.ColumnKeys {
						hv, _ := host.Cells[i][j].Value.(float64)
						oc := mx.Cells[i][j]
						if math.IsNaN(hv) {
							if fmt.Sprintf("%v/%v/%v", rk[0], rk[1], ck[0]) != "0/1/1" {
								t.Fatalf("%s: unexpected NaN host cell %v/%v", arm, rk, ck)
							}
							if oc.Present {
								t.Errorf("%s: the undefined cell scored %v, want it absent", arm, oc.Value)
							}
							continue
						}
						ov, ok := oc.Value.(float64)
						if !oc.Present || !ok || math.IsNaN(ov) || math.IsInf(ov, 0) {
							t.Errorf("%s: cell %v/%v = %+v, want a finite figure", arm, rk, ck, oc)
							continue
						}
						present++
					}
				}
				if present != 11 || layer.Summary == nil || layer.Summary.Count == nil || *layer.Summary.Count != present {
					t.Errorf("%s: %d scored cells, summary %+v, want 11 counted", arm, present, layer.Summary)
				}
				b, err := types.MarshalFinite(layer)
				if err != nil {
					t.Fatal(err)
				}
				layers = append(layers, string(b))
			}
			if layers[0] != layers[1] {
				t.Errorf("fused and buffered layers differ:\n%s\n%s", layers[0], layers[1])
			}
		})
	}
}

// TestComposeMatrixOverlays_UndefinedCellIsMissing: the Compose MATRIX
// arm of the cell-pair kinds reads an undefined target OR reference
// cell as a missing pair — the overlay cell absent, no warning, out of
// the summary — as the series arm does (E4-S5). Slot a's cell
// (0, 1, 1) and slot b's cell (1, 0, 2) are undefined; both
// directions run.
func TestComposeMatrixOverlays_UndefinedCellIsMissing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	fsys := afero.NewMemMapFs()
	writeUndefCellCohort(t, fsys, "u.pulse", [3]int{0, 1, 1})
	writeUndefCellCohort(t, fsys, "w.pulse", [3]int{1, 0, 2})
	p, err := pulse.New(pulse.Options{FS: fsys})
	if err != nil {
		t.Fatal(err)
	}
	twoSample := map[string]any{"variance_target": 4.0, "variance_ref": 4.0, "sample_size_target": 10.0, "sample_size_ref": 10.0}
	missing := map[string]bool{"0/1/1": true, "1/0/2": true}
	for _, dir := range []struct{ ref, target string }{{"a", "b"}, {"b", "a"}} {
		for _, c := range []struct {
			kind   types.OverlayKind
			params map[string]any
		}{
			{types.OverlayKindIndexVsRef, nil},
			{types.OverlayKindDeltaVsRef, nil},
			{types.OverlayKindPanelIndexVsRef, nil},
			{types.OverlayKindTCell, twoSample},
			{types.OverlayKindZCell, twoSample},
		} {
			t.Run(dir.ref+"->"+dir.target+"/"+string(c.kind), func(t *testing.T) {
				req := &pulse.ComposedRequest{
					Requests: []*types.Request{undefCellSlot("a", "u.pulse"), undefCellSlot("b", "w.pulse")},
					Overlays: []types.ComposeOverlaySpec{{Name: "o", Kind: c.kind, Reference: dir.ref, Targets: []string{dir.target}, Params: c.params}},
				}
				if env, err := p.PredictCompose(ctx, req); err != nil || len(env.Errors) != 0 {
					t.Fatalf("PredictCompose = %v %+v", err, env)
				}
				got, err := p.Compose(ctx, req)
				if err != nil {
					t.Fatal(err)
				}
				layer := got.Overlays[0]
				if len(layer.Warnings) != 0 {
					t.Errorf("warnings %+v, want none", layer.Warnings)
				}
				mx := layer.Payload.Matrix
				present := 0
				for i, rk := range mx.RowKeys {
					for j, ck := range mx.ColumnKeys {
						oc := mx.Cells[i][j]
						if missing[fmt.Sprintf("%v/%v/%v", rk[0], rk[1], ck[0])] {
							if oc.Present {
								t.Errorf("undefined pair %v/%v scored %v, want it absent", rk, ck, oc.Value)
							}
							continue
						}
						if ov, ok := oc.Value.(float64); !oc.Present || !ok || math.IsNaN(ov) {
							t.Errorf("cell %v/%v = %+v, want a figure", rk, ck, oc)
							continue
						}
						present++
					}
				}
				if present != 10 || layer.Summary == nil || layer.Summary.Count == nil || *layer.Summary.Count != present {
					t.Errorf("%d scored cells, summary %+v, want 10 counted", present, layer.Summary)
				}
			})
		}
	}
}
