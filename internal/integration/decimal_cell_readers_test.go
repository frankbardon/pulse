package integration_test

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// decimal_cell_readers_test.go (E4-S8): every reader of an aggregation
// cell other than the windows (E4-S1) reads a decimal128 cell as the
// float64 cell holding the same value. Two cohorts carry one column
// "amt" with the same values — decimal128 at scale 2 in one, f64 in the
// other — and each request's overlays, post-tests and crosstab
// cell reads must come out identical. Before, the opaque
// decimal cell fell through every numeric type switch: group / series
// overlays scored nothing (PULSE_OVERLAY_REF_ZERO), crosstab-host
// overlays and normalized cells were absent, every post-test refused
// PULSE_TEST_FIELD_NOT_NUMERIC, and types.MatrixCell.Scalar returned 0.
// (The chain hand-off and stage readers are pinned in
// internal/processing/decimal_cells_test.go.)

// decimalReaderCohort writes name with region g, segment h, day d and
// the amount amt, decimal128(18, 2) when dec, else f64 of equal value
// (every mantissa is a multiple of 25, so m / 100 is exact in binary).
func decimalReaderCohort(t *testing.T, p *pulse.Pulse, name string, dec bool) {
	t.Helper()
	amt := encoding.Field{Name: "amt", Type: encoding.FieldTypeF64, Description: "Amount charged on the order"}
	if dec {
		amt = encoding.Field{Name: "amt", Type: encoding.FieldTypeDecimal128, Precision: 18, Scale: 2, Description: "Amount charged on the order"}
	}
	schema := encoding.Schema{Fields: []encoding.Field{
		{Name: "g", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary(), Description: "Region of the order"},
		{Name: "h", Type: encoding.FieldTypeCategoricalU8, Dictionary: encoding.NewDictionary(), Description: "Segment of the order"},
		{Name: "d", Type: encoding.FieldTypeDate, Description: "Day the order was placed"},
		amt,
	}}
	b, err := p.NewCohortBuilder(context.Background(), name, schema, pulse.CohortBuilderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	regions, segments := []string{"n", "s", "e"}, []string{"a", "b"}
	for i := 0; i < 60; i++ {
		m := int64((i*37)%101*25 + 100)
		var a any = float64(m) / 100
		if dec {
			a = encoding.NewDecimal128FromInt(m)
		}
		if err := b.Append(pulse.CohortRow{regions[i%3], segments[(i/3)%2], int32(19000 + i*20), a}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Close(); err != nil {
		t.Fatal(err)
	}
}

func decimalReaderPulse(t *testing.T) *pulse.Pulse {
	t.Helper()
	p, err := pulse.New(pulse.Options{FS: afero.NewMemMapFs()})
	if err != nil {
		t.Fatal(err)
	}
	decimalReaderCohort(t, p, "f.pulse", false)
	decimalReaderCohort(t, p, "dec.pulse", true)
	return p
}

// scalarCells is the matrix's body as MatrixCell.Scalar reads it, NaN
// spelled out so two NaNs compare equal.
func scalarCells(m *types.MatrixPayload) [][]any {
	out := make([][]any, len(m.Cells))
	for i, row := range m.Cells {
		for _, c := range row {
			v := c.Scalar()
			if !c.Present {
				out[i] = append(out[i], "absent")
				continue
			}
			if math.IsNaN(v) {
				out[i] = append(out[i], "NaN")
				continue
			}
			out[i] = append(out[i], v)
		}
	}
	return out
}

func TestDecimalCells_ReadersMatchFloat64(t *testing.T) {
	t.Parallel()
	p := decimalReaderPulse(t)
	ctx := context.Background()
	month := `{"type":"GROUP_DATE","field":"d","params":{"component":"month","frequency":"monthly"}}`
	byRegion := `{"type":"GROUP_CATEGORY","field":"g"}`
	xtab := func(cell, extra string) string {
		return `{"crosstab":{"rows":[` + byRegion + `],"columns":[{"type":"GROUP_CATEGORY","field":"h"}],"cell":` + cell +
			`,"margins":{"rows":true,"columns":true,"grand":true}` + extra + `}`
	}
	cellOverlays := `,"overlays":[{"name":"i","kind":"OVERLAY_INDEX_VS_MARGIN","scope":"cell","ref":{"margin":{"axis":"row"}}},` +
		`{"name":"sr","kind":"OVERLAY_SHARE_OF_ROW","scope":"cell","ref":{"margin":{"axis":"row"}}},` +
		`{"name":"dm","kind":"OVERLAY_DELTA_VS_MARGIN","scope":"cell","ref":{"margin":{"axis":"row"}}}]}`
	cases := map[string]string{
		// Group / series overlays (toFloat64).
		"group_overlays": `{"aggregations":[{"type":"AGG_SUM","field":"amt","label":"s"}],"groups":[` + byRegion + `],` +
			`"overlays":[{"name":"i","kind":"OVERLAY_INDEX_VS_TOTAL","scope":"group"},` +
			`{"name":"ds","kind":"OVERLAY_DELTA_VS_SIBLING","scope":"group","ref":{"sibling":{"field":"g","value":"n"}}}]}`,
		"series_overlays": `{"aggregations":[{"type":"AGG_AVERAGE","field":"amt","label":"s"}],"groups":[` + month + `],` +
			`"overlays":[{"name":"z","kind":"OVERLAY_ZSCORE_VS_ROLLING","scope":"group","ref":{"rolling_mean":{}},"params":{"window":3}},` +
			`{"name":"f","kind":"OVERLAY_FORMULA","scope":"group","params":{"formula":"(value - prior) / total * 100"}},` +
			`{"name":"yoy","kind":"OVERLAY_YOY","scope":"group","ref":{"yoy":{}}}]}`,
		// Crosstab-host overlays (scalarFromCell) over a sum and a mean.
		"cell_overlays_sum": xtab(`{"type":"AGG_SUM","field":"amt","label":"s"}`, "") + cellOverlays,
		"cell_overlays_avg": xtab(`{"type":"AGG_AVERAGE","field":"amt","label":"s"}`, "") + cellOverlays,
		// Crosstab normalization (coerceFloat64).
		"normalize_row":    xtab(`{"type":"AGG_SUM","field":"amt","label":"s"}`, `,"normalize":"row"`) + `}`,
		"normalize_column": xtab(`{"type":"AGG_SUM","field":"amt","label":"s"}`, `,"normalize":"column"`) + `}`,
		// Post-tests (floatValue).
		"post_anova": `{"aggregations":[{"type":"AGG_AVERAGE","field":"amt","label":"m"},{"type":"AGG_VARIANCE","field":"amt","label":"v"},` +
			`{"type":"AGG_COUNT","field":"amt","label":"n"}],"groups":[` + byRegion + `],` +
			`"post_tests":[{"type":"TEST_ANOVA_F","field":"m","split_by":"g","label":"an","params":{"n_col":"n","variance_col":"v"}}]}`,
		"post_trend_pearson": `{"aggregations":[{"type":"AGG_SUM","field":"amt","label":"s"},{"type":"AGG_AVERAGE","field":"amt","label":"c"}],` +
			`"groups":[` + month + `],"post_tests":[{"type":"TEST_TREND","field":"s","label":"mk","order_by":[{"field":"d"}],"params":{"variant":"mann_kendall"}},` +
			`{"type":"TEST_PEARSON_R","field":"s","field2":"c","label":"pr"}]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			var out [2]string
			for i, file := range []string{"f.pulse", "dec.pulse"} {
				var req types.Request
				if err := json.Unmarshal([]byte(body), &req); err != nil {
					t.Fatal(err)
				}
				req.Cohort = &types.Cohort{Filename: file}
				resp, err := p.Process(ctx, &req)
				if err != nil {
					t.Fatalf("%s: %v", file, err)
				}
				view := map[string]any{"overlays": resp.Overlays, "post_tests": resp.PostTests, "warnings": resp.Warnings}
				if resp.Crosstab != nil {
					view["cells"] = scalarCells(resp.Crosstab.Matrix)
				}
				b, err := types.MarshalFinite(view)
				if err != nil {
					t.Fatal(err)
				}
				out[i] = string(b)
			}
			if out[0] != out[1] {
				t.Errorf("decimal cells read unlike float64 cells:\n float64 %s\n decimal %s", out[0], out[1])
			}
			if len(out[0]) < 150 {
				t.Errorf("the float64 run carries too little to compare: %s", out[0])
			}
		})
	}
}

// TestDecimalCells_MatrixCellScalar: the public MatrixCell.Scalar reads
// a decimal aggregate cell as its float64 value, where it returned 0.
func TestDecimalCells_MatrixCellScalar(t *testing.T) {
	t.Parallel()
	p := decimalReaderPulse(t)
	var cells [2][][]any
	for i, file := range []string{"f.pulse", "dec.pulse"} {
		req := &types.Request{Cohort: &types.Cohort{Filename: file}, Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "h"}},
			Cell:    &types.Aggregation{Type: types.AGG_AVERAGE, Field: "amt", Label: "m"},
		}}
		resp, err := p.Process(context.Background(), req)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		cells[i] = scalarCells(resp.Crosstab.Matrix)
	}
	a, _ := json.Marshal(cells[0])
	b, _ := json.Marshal(cells[1])
	if string(a) != string(b) {
		t.Errorf("Scalar over decimal cells %s, over float64 cells %s", b, a)
	}
	if string(a) == "[]" {
		t.Fatal("no cells")
	}
}
