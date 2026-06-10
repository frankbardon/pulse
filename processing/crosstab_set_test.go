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

// TestCrosstab_MapValuedCellRejectsNormalize verifies normalize × map
// cell aggregator combo raises PULSE_CROSSTAB_NORMALIZE_MAP_VALUED. The
// runtime gate runs before any bucket aggregation, so even minimal
// fixtures must surface the error.
func TestCrosstab_MapValuedCellRejectsNormalize(t *testing.T) {
	schema := crosstabSetSchema(t)
	recs := []*Record{crosstabSetRecord(schema, 0, 0b0001)}
	for _, mode := range []types.CrosstabNormalize{
		types.CrosstabNormalizeRow,
		types.CrosstabNormalizeColumn,
		types.CrosstabNormalizeTotal,
	} {
		_, err := runMapCellCrosstab(t, recs, mode)
		if err == nil {
			t.Errorf("normalize=%s: expected PULSE_CROSSTAB_NORMALIZE_MAP_VALUED, got nil", mode)
			continue
		}
		if !strings.Contains(err.Error(), "PULSE_CROSSTAB_NORMALIZE_MAP_VALUED") &&
			!strings.Contains(err.Error(), "map-valued") {
			t.Errorf("normalize=%s: error = %v, want PULSE_CROSSTAB_NORMALIZE_MAP_VALUED", mode, err)
		}
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
