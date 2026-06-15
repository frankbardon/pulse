package processing

import (
	"context"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// crosstabSetSchema builds a 2-field schema: a categorical "region"
// (north / south) on the row axis and a 4-issuer "tags" set on the
// cell aggregator. Used by every map-cell crosstab test in this file.
func crosstabSetSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	regionDict := encoding.NewDictionary()
	for _, r := range []string{"north", "south"} {
		if _, err := regionDict.Add(r); err != nil {
			t.Fatalf("region dict.Add: %v", err)
		}
	}
	tagsDict := encoding.NewDictionary()
	for _, v := range []string{"VISA", "MC", "AMEX", "DISC"} {
		if _, err := tagsDict.Add(v); err != nil {
			t.Fatalf("tags dict.Add: %v", err)
		}
	}
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: regionDict},
			{Name: "tags", Type: encoding.FieldTypeSetU8, Dictionary: tagsDict},
		},
	}
}

// crosstabSetRecord builds a Record with the given region id (categorical
// index 0=north, 1=south) and tags mask.
func crosstabSetRecord(schema *encoding.Schema, region uint64, mask uint64) *Record {
	return NewRecordWithWide(schema,
		map[string]float64{"region": float64(region)},
		nil,
		map[string]any{"tags": mask})
}

func runMapCellCrosstab(t *testing.T, recs []*Record, normalize types.CrosstabNormalize) (*types.Response, error) {
	t.Helper()
	schema := crosstabSetSchema(t)
	p := NewProcessor(schema)
	req := &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Cell:    &types.Aggregation{Type: types.AGG_SET_FREQUENCY, Field: "tags", Label: "tag_counts"},
			Shape:   types.CrosstabShapeMatrix,
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		},
	}
	if normalize != "" {
		req.Crosstab.Normalize = normalize
	}
	return p.RunCrosstab(context.Background(), req, recs)
}

// TestCrosstab_SetFrequencyCellEmitsMap verifies that AGG_SET_FREQUENCY
// in a Crosstab Cell populates MatrixCell.Value with the per-label
// row-count map (not the scalar fallback).
func TestCrosstab_SetFrequencyCellEmitsMap(t *testing.T) {
	schema := crosstabSetSchema(t)
	// 3 north records: 2× {VISA,MC}, 1× {AMEX}. Diagonal (north,north)
	// cell carries all north records.
	recs := []*Record{
		crosstabSetRecord(schema, 0, 0b0011),
		crosstabSetRecord(schema, 0, 0b0011),
		crosstabSetRecord(schema, 0, 0b0100),
	}
	p := NewProcessor(schema)
	req := &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Cell:    &types.Aggregation{Type: types.AGG_SET_FREQUENCY, Field: "tags", Label: "tag_counts"},
			Shape:   types.CrosstabShapeMatrix,
		},
	}
	resp, err := p.RunCrosstab(context.Background(), req, recs)
	if err != nil {
		t.Fatalf("RunCrosstab: %v", err)
	}
	if resp.Crosstab == nil || resp.Crosstab.Matrix == nil {
		t.Fatal("expected matrix payload")
	}
	cell := resp.Crosstab.Matrix.Cells[0][0]
	if !cell.Present {
		t.Fatal("(north,north) cell not present")
	}
	counts, ok := cell.Value.(map[string]int)
	if !ok {
		t.Fatalf("cell.Value = %T (%v), want map[string]int", cell.Value, cell.Value)
	}
	if counts["VISA"] != 2 || counts["MC"] != 2 || counts["AMEX"] != 1 {
		t.Errorf("counts = %v, want VISA=2 MC=2 AMEX=1", counts)
	}
	if _, present := counts["DISC"]; present {
		t.Errorf("zero-count label DISC must not appear: %v", counts)
	}
}

// TestCrosstab_SetFrequencyMarginEmitsMap verifies row/column/grand
// margins recomputed for AGG_SET_FREQUENCY also surface the map payload
// (per the margin path running the same aggregator over a wider bucket).
func TestCrosstab_SetFrequencyMarginEmitsMap(t *testing.T) {
	schema := crosstabSetSchema(t)
	recs := []*Record{
		crosstabSetRecord(schema, 0, 0b0011), // north: VISA + MC
		crosstabSetRecord(schema, 1, 0b0100), // south: AMEX
	}
	resp, err := runMapCellCrosstab(t, recs, types.CrosstabNormalizeNone)
	if err != nil {
		t.Fatalf("RunCrosstab: %v", err)
	}
	m := resp.Crosstab.Matrix

	// Row margin for north: VISA + MC each appear once.
	rowMargin := m.RowMargins[0]
	if !rowMargin.Present {
		t.Fatal("row margin [0] missing")
	}
	rcounts, ok := rowMargin.Value.(map[string]int)
	if !ok {
		t.Fatalf("row margin Value = %T, want map[string]int", rowMargin.Value)
	}
	if rcounts["VISA"] != 1 || rcounts["MC"] != 1 {
		t.Errorf("north row margin = %v, want VISA=1 MC=1", rcounts)
	}

	// Grand margin spans all 3 unique bits — VISA, MC each 1, AMEX 1.
	grand := m.GrandTotal
	if !grand.Present {
		t.Fatal("grand margin missing")
	}
	gcounts, ok := grand.Value.(map[string]int)
	if !ok {
		t.Fatalf("grand Value = %T, want map[string]int", grand.Value)
	}
	if gcounts["VISA"] != 1 || gcounts["MC"] != 1 || gcounts["AMEX"] != 1 {
		t.Errorf("grand margin = %v, want VISA=1 MC=1 AMEX=1", gcounts)
	}
}

// crosstabWelfordSchema builds a 2-field schema: a categorical "region"
// (north / south) on both crosstab axes and a numeric float64 "value"
// field for the Welford cell aggregator. Welford rejects categorical
// and decimal field types at factory time, so the cell field must be
// drawn from the strict scalar numeric family.
func crosstabWelfordSchema(t *testing.T) *encoding.Schema {
	t.Helper()
	regionDict := encoding.NewDictionary()
	for _, r := range []string{"north", "south"} {
		if _, err := regionDict.Add(r); err != nil {
			t.Fatalf("region dict.Add: %v", err)
		}
	}
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: regionDict},
			{Name: "value", Type: encoding.FieldTypeF64},
		},
	}
}

// crosstabWelfordRecord builds a Record carrying the given region id
// (categorical index 0=north, 1=south) and a float64 sample for the
// Welford accumulator.
func crosstabWelfordRecord(schema *encoding.Schema, region uint64, value float64) *Record {
	return NewRecordWithNulls(schema,
		map[string]float64{"region": float64(region), "value": value},
		nil)
}

// runWelfordCellCrosstab wires a crosstab Request with AGG_WELFORD on
// the numeric "value" field and the given normalize mode, then dispatches
// through RunCrosstab. Used by the map-valued normalize-rejection rows
// and the normalize=none Rich-payload happy path.
func runWelfordCellCrosstab(t *testing.T, recs []*Record, normalize types.CrosstabNormalize) (*types.Response, error) {
	t.Helper()
	schema := crosstabWelfordSchema(t)
	p := NewProcessor(schema)
	req := &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Cell:    &types.Aggregation{Type: types.AGG_WELFORD, Field: "value", Label: "welford"},
			Shape:   types.CrosstabShapeMatrix,
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		},
	}
	if normalize != "" {
		req.Crosstab.Normalize = normalize
	}
	return p.RunCrosstab(context.Background(), req, recs)
}

// TestCrosstab_MapValuedCellRejectsNormalize verifies normalize × map
// cell aggregator combo raises PULSE_CROSSTAB_NORMALIZE_MAP_VALUED. The
// runtime gate runs before any bucket aggregation, so even minimal
// fixtures must surface the error.
//
// Covered map-valued aggregators (one subtest tree each):
//
//   - AGG_SET_FREQUENCY (per-label row-count map)
//   - AGG_WELFORD       (WelfordTriple — mean / variance / n)
//
// Both are classified MapValued()=true in types/streamability.go; the
// runtime gate at processing/crosstab.go validateCrosstabSpec must reject
// every normalize axis for either aggregator.
func TestCrosstab_MapValuedCellRejectsNormalize(t *testing.T) {
	normalizeModes := []types.CrosstabNormalize{
		types.CrosstabNormalizeRow,
		types.CrosstabNormalizeColumn,
		types.CrosstabNormalizeTotal,
	}

	t.Run("AGG_SET_FREQUENCY", func(t *testing.T) {
		schema := crosstabSetSchema(t)
		recs := []*Record{crosstabSetRecord(schema, 0, 0b0001)}
		for _, mode := range normalizeModes {
			mode := mode
			t.Run(string(mode), func(t *testing.T) {
				_, err := runMapCellCrosstab(t, recs, mode)
				if err == nil {
					t.Fatalf("normalize=%s: expected PULSE_CROSSTAB_NORMALIZE_MAP_VALUED, got nil", mode)
				}
				if !strings.Contains(err.Error(), "PULSE_CROSSTAB_NORMALIZE_MAP_VALUED") &&
					!strings.Contains(err.Error(), "map-valued") {
					t.Fatalf("normalize=%s: error = %v, want PULSE_CROSSTAB_NORMALIZE_MAP_VALUED", mode, err)
				}
			})
		}
	})

	t.Run("AGG_WELFORD", func(t *testing.T) {
		schema := crosstabWelfordSchema(t)
		recs := []*Record{crosstabWelfordRecord(schema, 0, 1.0)}
		for _, mode := range normalizeModes {
			mode := mode
			t.Run(string(mode), func(t *testing.T) {
				_, err := runWelfordCellCrosstab(t, recs, mode)
				if err == nil {
					t.Fatalf("normalize=%s: expected PULSE_CROSSTAB_NORMALIZE_MAP_VALUED, got nil", mode)
				}
				if !strings.Contains(err.Error(), "PULSE_CROSSTAB_NORMALIZE_MAP_VALUED") &&
					!strings.Contains(err.Error(), "map-valued") {
					t.Fatalf("normalize=%s: error = %v, want PULSE_CROSSTAB_NORMALIZE_MAP_VALUED", mode, err)
				}
			})
		}
	})
}

// TestCrosstab_WelfordCellNormalizeNoneEmitsRich verifies the happy path:
// AGG_WELFORD on a Crosstab cell with normalize=none (the default) must
// succeed. Post-E3-S8 the MatrixCell.Value surface carries the SCALAR
// mean (matching welfordAggregator.Aggregate / Finalize) — the full
// {mean, variance, m2, stddev, n, n_null} triple now lives in
// Response.Components.Crosstab.CellComponents[r][c] via the
// MetaAggregator pass. Pairs with TestCrosstab_MapValuedCellRejectsNormalize
// to bracket the runtime gate from both sides (normalize=none accepted,
// normalize != none rejected).
func TestCrosstab_WelfordCellNormalizeNoneEmitsRich(t *testing.T) {
	schema := crosstabWelfordSchema(t)
	// 3 north records on the (north,north) diagonal: samples {2, 4, 6}
	// → mean = 4, sample variance = 4 (M2 = 8, n-1 = 2), n = 3.
	recs := []*Record{
		crosstabWelfordRecord(schema, 0, 2.0),
		crosstabWelfordRecord(schema, 0, 4.0),
		crosstabWelfordRecord(schema, 0, 6.0),
	}
	resp, err := runWelfordCellCrosstab(t, recs, types.CrosstabNormalizeNone)
	if err != nil {
		t.Fatalf("RunCrosstab: %v", err)
	}
	if resp.Crosstab == nil || resp.Crosstab.Matrix == nil {
		t.Fatal("expected matrix payload")
	}
	cell := resp.Crosstab.Matrix.Cells[0][0]
	if !cell.Present {
		t.Fatal("(north,north) cell not present")
	}
	// E3-S8: MatrixCell.Value carries the scalar mean (Aggregate /
	// Finalize return value); the WelfordTriple payload no longer rides
	// here. Sanity-check the type is plain float64.
	mean, ok := cell.Value.(float64)
	if !ok {
		t.Fatalf("cell.Value = %T (%v), want float64 (scalar mean after E3-S8)", cell.Value, cell.Value)
	}
	if mean != 4.0 {
		t.Errorf("cell.Value (scalar mean) = %v, want 4.0", mean)
	}
	if _, isTriple := cell.Value.(WelfordTriple); isTriple {
		t.Fatalf("cell.Value carries WelfordTriple — E3-S8 retired the smuggling; expect scalar float64 mean")
	}
	// The full triple lives in CellComponents — verify the components
	// pass populated mean / variance / n alongside the universal floor.
	if resp.Components == nil || resp.Components.Crosstab == nil {
		t.Fatal("Response.Components.Crosstab missing — components pass did not fire")
	}
	cellComps := resp.Components.Crosstab.CellComponents
	if len(cellComps) == 0 || len(cellComps[0]) == 0 || cellComps[0][0] == nil {
		t.Fatal("CellComponents[0][0] missing — Welford components not routed")
	}
	comp := cellComps[0][0]
	if got := comp["mean"]; got != 4.0 {
		t.Errorf("CellComponents[0][0][mean] = %v, want 4.0", got)
	}
	if got := comp["variance"]; got != 4.0 {
		t.Errorf("CellComponents[0][0][variance] = %v, want 4.0 (M2=8 / (n-1)=2)", got)
	}
	if got := comp["n"]; got != 3 {
		t.Errorf("CellComponents[0][0][n] = %v, want 3", got)
	}
}

// TestCrosstab_WelfordCellValueIsScalarNotTriple is the dedicated
// E3-S8 contract gate: AGG_WELFORD cell aggregators MUST write the
// scalar mean into MatrixCell.Value (NOT the WelfordTriple) for every
// (row, col) tuple, every row margin, every column margin, and the
// grand total. The triple stays available exclusively via
// Response.Components.Crosstab.CellComponents[r][c]. Locks the
// dispatcher carve-out in processor.go (dispatchAggregatorCellResult)
// against accidental regression to the legacy Rich-or-scalar lift used
// by Response.Data rows.
func TestCrosstab_WelfordCellValueIsScalarNotTriple(t *testing.T) {
	schema := crosstabWelfordSchema(t)
	// Two cohort buckets — diagonal cells (0,0) and (1,1) populated so
	// row / column / grand margins all have non-empty input. Samples
	// per cell: {2, 4, 6} → mean 4, variance 4, n 3.
	recs := []*Record{
		crosstabWelfordRecord(schema, 0, 2.0),
		crosstabWelfordRecord(schema, 0, 4.0),
		crosstabWelfordRecord(schema, 0, 6.0),
		crosstabWelfordRecord(schema, 1, 2.0),
		crosstabWelfordRecord(schema, 1, 4.0),
		crosstabWelfordRecord(schema, 1, 6.0),
	}
	p := NewProcessor(schema)
	req := &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Cell:    &types.Aggregation{Type: types.AGG_WELFORD, Field: "value", Label: "welford"},
			Shape:   types.CrosstabShapeMatrix,
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		},
	}
	resp, err := p.RunCrosstab(context.Background(), req, recs)
	if err != nil {
		t.Fatalf("RunCrosstab: %v", err)
	}
	if resp.Crosstab == nil || resp.Crosstab.Matrix == nil {
		t.Fatal("expected matrix payload")
	}
	mx := resp.Crosstab.Matrix
	// Every present cell, every row margin, every column margin, and the
	// grand total MUST be float64 — not WelfordTriple.
	for i, row := range mx.Cells {
		for j, cell := range row {
			if !cell.Present {
				continue
			}
			if _, isTriple := cell.Value.(WelfordTriple); isTriple {
				t.Errorf("Cells[%d][%d].Value carries WelfordTriple — E3-S8 contract requires scalar mean", i, j)
			}
			if _, ok := cell.Value.(float64); !ok {
				t.Errorf("Cells[%d][%d].Value = %T, want float64 (scalar mean)", i, j, cell.Value)
			}
		}
	}
	for i, m := range mx.RowMargins {
		if !m.Present {
			continue
		}
		if _, isTriple := m.Value.(WelfordTriple); isTriple {
			t.Errorf("RowMargins[%d].Value carries WelfordTriple — E3-S8 contract requires scalar mean", i)
		}
		if _, ok := m.Value.(float64); !ok {
			t.Errorf("RowMargins[%d].Value = %T, want float64", i, m.Value)
		}
	}
	for i, m := range mx.ColumnMargins {
		if !m.Present {
			continue
		}
		if _, isTriple := m.Value.(WelfordTriple); isTriple {
			t.Errorf("ColumnMargins[%d].Value carries WelfordTriple — E3-S8 contract requires scalar mean", i)
		}
		if _, ok := m.Value.(float64); !ok {
			t.Errorf("ColumnMargins[%d].Value = %T, want float64", i, m.Value)
		}
	}
	if mx.GrandTotal.Present {
		if _, isTriple := mx.GrandTotal.Value.(WelfordTriple); isTriple {
			t.Error("GrandTotal.Value carries WelfordTriple — E3-S8 contract requires scalar mean")
		}
		if _, ok := mx.GrandTotal.Value.(float64); !ok {
			t.Errorf("GrandTotal.Value = %T, want float64", mx.GrandTotal.Value)
		}
	}
	// The triple lives in CellComponents — verify at least the diagonal
	// cell carries it so the E3-S7 components-source overlay path still
	// has its input.
	if resp.Components == nil || resp.Components.Crosstab == nil {
		t.Fatal("Response.Components.Crosstab missing — components pass did not fire")
	}
	cellComps := resp.Components.Crosstab.CellComponents
	if len(cellComps) == 0 || len(cellComps[0]) == 0 || cellComps[0][0] == nil {
		t.Fatal("CellComponents[0][0] missing — Welford components not routed")
	}
	if got := cellComps[0][0]["mean"]; got != 4.0 {
		t.Errorf("CellComponents[0][0][mean] = %v, want 4.0", got)
	}
	if got := cellComps[0][0]["variance"]; got != 4.0 {
		t.Errorf("CellComponents[0][0][variance] = %v, want 4.0", got)
	}
}

// TestCrosstab_ScalarCellPathUnchanged guards backward compatibility:
// a scalar cell aggregator round-trips through the widened MatrixCell
// shape with the same float64 value (cell.Value asserts to float64).
func TestCrosstab_ScalarCellPathUnchanged(t *testing.T) {
	schema := crosstabSetSchema(t)
	recs := []*Record{
		crosstabSetRecord(schema, 0, 0b0011),
		crosstabSetRecord(schema, 0, 0b0001),
	}
	p := NewProcessor(schema)
	req := &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Cell:    &types.Aggregation{Type: types.AGG_SET_CARDINALITY_SUM, Field: "tags", Label: "popsum"},
			Shape:   types.CrosstabShapeMatrix,
		},
	}
	resp, err := p.RunCrosstab(context.Background(), req, recs)
	if err != nil {
		t.Fatalf("RunCrosstab: %v", err)
	}
	cell := resp.Crosstab.Matrix.Cells[0][0]
	if !cell.Present {
		t.Fatal("(north,north) cell not present")
	}
	got, ok := cell.Value.(float64)
	if !ok {
		t.Fatalf("scalar aggregator cell.Value = %T, want float64", cell.Value)
	}
	// popcount(0b0011) + popcount(0b0001) = 2 + 1 = 3
	if got != 3 {
		t.Errorf("popsum = %v, want 3", got)
	}
	if cell.Scalar() != 3 {
		t.Errorf("Scalar() = %v, want 3", cell.Scalar())
	}
}

// TestCrosstab_SetPerElementOnRowAxis verifies GROUP_SET_PER_ELEMENT on
// a crosstab row axis correctly composes with the existing multi-grouper
// partitioning: a single input row fans into one row bucket per set bit
// it carries, and each fanned copy is independently partitioned by the
// column axis. PR 65 acknowledged set groupers on crosstab axes as
// "should work through existing multi-grouper plumbing but untested" —
// this test pins the property so a refactor of partitionByAxis or the
// processor bucket loop cannot silently regress per-element fan-out on
// axes.
func TestCrosstab_SetPerElementOnRowAxis(t *testing.T) {
	schema := crosstabSetSchema(t)
	// 4 records varying region and tag mask.
	//
	//   R1: north, {VISA, MC}     (fans into 2 row buckets: VISA, MC)
	//   R2: north, {AMEX}         (fans into 1 row bucket : AMEX)
	//   R3: south, {VISA}         (fans into 1 row bucket : VISA)
	//   R4: south, {}             (empty mask → fans into NOTHING)
	recs := []*Record{
		crosstabSetRecord(schema, 0, 0b0011),
		crosstabSetRecord(schema, 0, 0b0100),
		crosstabSetRecord(schema, 1, 0b0001),
		crosstabSetRecord(schema, 1, 0b0000),
	}
	p := NewProcessor(schema)
	req := &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}},
			Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "region", Label: "n"},
			Shape:   types.CrosstabShapeMatrix,
		},
	}
	resp, err := p.RunCrosstab(context.Background(), req, recs)
	if err != nil {
		t.Fatalf("RunCrosstab: %v", err)
	}
	m := resp.Crosstab.Matrix
	if m == nil {
		t.Fatal("expected matrix payload")
	}

	rowIdx := axisKeyIndex(m.RowKeys)
	colIdx := axisKeyIndex(m.ColumnKeys)

	type cellExp struct {
		row, col string
		want     float64
	}
	wantCells := []cellExp{
		// VISA row gets R1 (north) + R3 (south).
		{row: "VISA", col: "north", want: 1},
		{row: "VISA", col: "south", want: 1},
		// MC row gets R1 (north only).
		{row: "MC", col: "north", want: 1},
		// AMEX row gets R2 (north only).
		{row: "AMEX", col: "north", want: 1},
	}
	for _, c := range wantCells {
		ri, ok := rowIdx[c.row]
		if !ok {
			t.Errorf("row %q missing from RowKeys %v", c.row, m.RowKeys)
			continue
		}
		ci, ok := colIdx[c.col]
		if !ok {
			t.Errorf("col %q missing from ColumnKeys %v", c.col, m.ColumnKeys)
			continue
		}
		cell := m.Cells[ri][ci]
		if !cell.Present {
			t.Errorf("cell (%s,%s) not present", c.row, c.col)
			continue
		}
		if cell.Scalar() != c.want {
			t.Errorf("cell (%s,%s) count: got %v, want %v", c.row, c.col, cell.Scalar(), c.want)
		}
	}

	// Empty-mask row contributes zero cells; the DISC bit is never set
	// in any record, so it must not appear as a row key.
	for _, k := range m.RowKeys {
		if len(k) != 1 {
			t.Errorf("row key %v has unexpected arity", k)
			continue
		}
		label, _ := k[0].(string)
		if label == "DISC" {
			t.Errorf("DISC must not appear as row key (no record holds it): %v", m.RowKeys)
		}
	}

	// Total set-bit fan-out must equal the sum of present cell scalars.
	// ∑ popcount(mask) across all rows = 2 + 1 + 1 + 0 = 4.
	var totalCount float64
	for ri := range m.Cells {
		for ci := range m.Cells[ri] {
			if m.Cells[ri][ci].Present {
				totalCount += m.Cells[ri][ci].Scalar()
			}
		}
	}
	if totalCount != 4 {
		t.Errorf("sum of present cell counts: got %v, want 4 (∑ popcount across rows)", totalCount)
	}
}

// TestCrosstab_SetPerElementOnBothAxes pins the "no double-counting
// under dual fan-out" property of GROUP_SET_PER_ELEMENT used on BOTH
// axes simultaneously. A row with popcount=k contributes to k² cells
// (k row buckets × k column buckets), and cells (a,b) where a row
// doesn't hold both a and b must be absent — the per-axis fan-out is
// independent, not cartesian-over-the-source-mask.
func TestCrosstab_SetPerElementOnBothAxes(t *testing.T) {
	schema := crosstabSetSchema(t)
	// R1: {VISA, MC}  → contributes to (VISA,VISA) (VISA,MC) (MC,VISA) (MC,MC) → 4 cells
	// R2: {AMEX}      → contributes to (AMEX,AMEX)                           → 1 cell
	// R3: {VISA}      → contributes to (VISA,VISA)                            → 1 cell
	// R4: {}          → contributes to NO cell
	recs := []*Record{
		crosstabSetRecord(schema, 0, 0b0011),
		crosstabSetRecord(schema, 0, 0b0100),
		crosstabSetRecord(schema, 1, 0b0001),
		crosstabSetRecord(schema, 1, 0b0000),
	}
	p := NewProcessor(schema)
	req := &types.Request{
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}},
			Columns: []*types.Group{{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "region", Label: "n"},
			Shape:   types.CrosstabShapeMatrix,
		},
	}
	resp, err := p.RunCrosstab(context.Background(), req, recs)
	if err != nil {
		t.Fatalf("RunCrosstab: %v", err)
	}
	m := resp.Crosstab.Matrix
	if m == nil {
		t.Fatal("expected matrix payload")
	}

	rowIdx := axisKeyIndex(m.RowKeys)
	colIdx := axisKeyIndex(m.ColumnKeys)

	// Expected present cells with their counts.
	type cellExp struct {
		row, col string
		want     float64
	}
	wantCells := []cellExp{
		{row: "VISA", col: "VISA", want: 2}, // R1 + R3
		{row: "VISA", col: "MC", want: 1},   // R1
		{row: "MC", col: "VISA", want: 1},   // R1
		{row: "MC", col: "MC", want: 1},     // R1
		{row: "AMEX", col: "AMEX", want: 1}, // R2
	}
	wantPresent := make(map[[2]string]float64, len(wantCells))
	for _, c := range wantCells {
		wantPresent[[2]string{c.row, c.col}] = c.want
	}

	for ri, rk := range m.RowKeys {
		for ci, ck := range m.ColumnKeys {
			rLabel := rk[0].(string)
			cLabel := ck[0].(string)
			cell := m.Cells[ri][ci]
			want, expected := wantPresent[[2]string{rLabel, cLabel}]
			if expected {
				if !cell.Present {
					t.Errorf("cell (%s,%s) expected present but absent", rLabel, cLabel)
					continue
				}
				if cell.Scalar() != want {
					t.Errorf("cell (%s,%s) count: got %v, want %v", rLabel, cLabel, cell.Scalar(), want)
				}
			} else if cell.Present {
				// Negative assertion: no record holds BOTH labels, so cell must not be present.
				t.Errorf("cell (%s,%s) unexpectedly present (count=%v); no record holds both labels",
					rLabel, cLabel, cell.Scalar())
			}
		}
	}

	// Defensive: re-confirm via grand total.
	// ∑ popcount² across rows = 2² + 1² + 1² + 0² = 6.
	var totalCount float64
	for ri := range m.Cells {
		for ci := range m.Cells[ri] {
			if m.Cells[ri][ci].Present {
				totalCount += m.Cells[ri][ci].Scalar()
			}
		}
	}
	if totalCount != 6 {
		t.Errorf("sum of present cell counts: got %v, want 6 (∑ popcount² across rows)", totalCount)
	}

	// DISC bit is never set in any record — must not appear on either axis.
	for _, k := range m.RowKeys {
		if label, _ := k[0].(string); label == "DISC" {
			t.Errorf("DISC must not appear as row key: %v", m.RowKeys)
		}
	}
	for _, k := range m.ColumnKeys {
		if label, _ := k[0].(string); label == "DISC" {
			t.Errorf("DISC must not appear as column key: %v", m.ColumnKeys)
		}
	}

	// rowIdx/colIdx live only to fail-loudly if axis-key sort order
	// changes — both maps are populated above.
	_ = rowIdx
	_ = colIdx
}

// axisKeyIndex builds a label → index map from a list of single-grouper
// AxisKey tuples. Used by the set-axis crosstab tests to look up cells
// by row/column label regardless of sort order.
func axisKeyIndex(keys []types.AxisKey) map[string]int {
	out := make(map[string]int, len(keys))
	for i, k := range keys {
		if len(k) == 0 {
			continue
		}
		if label, ok := k[0].(string); ok {
			out[label] = i
		}
	}
	return out
}

// TestCrosstab_RichDispatchInBufferedAggregate verifies the
// dispatchAggregatorResult helper routes RichAggregator output into
// Response.Data when no Crosstab section is in play (plain grouped
// process). AGG_SET_UNION returns []string from Rich(); the response
// row's "union" column must carry the slice, not a float64 scalar.
func TestCrosstab_RichDispatchInBufferedAggregate(t *testing.T) {
	schema := crosstabSetSchema(t)
	recs := []*Record{
		crosstabSetRecord(schema, 0, 0b0001), // VISA
		crosstabSetRecord(schema, 0, 0b0110), // MC + AMEX
	}
	p := NewProcessor(schema)
	row, err := p.aggregate([]*types.Aggregation{{
		Type:  types.AGG_SET_UNION,
		Field: "tags",
		Label: "union",
	}}, recs)
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	got, ok := row["union"].([]string)
	if !ok {
		t.Fatalf("row[\"union\"] = %T (%v), want []string from RichAggregator", row["union"], row["union"])
	}
	want := map[string]bool{"VISA": true, "MC": true, "AMEX": true}
	if len(got) != len(want) {
		t.Fatalf("labels = %v, want %v", got, want)
	}
	for _, lbl := range got {
		if !want[lbl] {
			t.Errorf("unexpected label %q in %v", lbl, got)
		}
	}
}
