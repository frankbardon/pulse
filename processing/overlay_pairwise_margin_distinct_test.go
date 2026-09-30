package processing

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// Unit coverage for the E1-S4 margin distinct-key sample sizes.
//
// The property under test is that a MARGIN distinct count is EXACT
// where a cell-sum is not. A margin recomputes the cell aggregator over
// the raw records that reached the margin key — once each — whereas
// summing per-cell distinct cardinalities counts a key once per cell it
// reached. Under a fan-out column axis those two disagree, and the
// margin is the right one. That is also why these modes are NOT subject
// to the E1-S3 partition gate.

// pairwiseMarginDistinctHost is a ONE-dim row axis (segment) × a
// fan-out column axis (brand, multi-select), with an AGG_DISTINCT_SUM
// component shape {sum, distinct_count} on every cell and on every
// margin slot.
//
// Respondents (each contributing TWO records):
//
//	101 urban {acme, zenith}   102 urban {acme}    104 urban {zenith}
//	103 rural {acme}           105 rural {zenith}
//
// Row `urban` therefore holds THREE distinct respondents but its two
// cells carry two each — 101 is in both, because `brand` fans out. The
// naive cell-sum says 4; the margin says 3.
func pairwiseMarginDistinctHost() *CrosstabHostView {
	mx := &types.MatrixPayload{
		RowHeader:    types.AxisHeader{Fields: []string{"segment"}, Types: []string{"GROUP_CATEGORY"}},
		ColumnHeader: types.AxisHeader{Fields: []string{"brand"}, Types: []string{"GROUP_SET_PER_ELEMENT"}},
		RowKeys:      []types.AxisKey{{"urban"}, {"rural"}},
		ColumnKeys:   []types.AxisKey{{"acme"}, {"zenith"}},
		Cells: [][]types.MatrixCell{
			{{Value: 80.0, Present: true}, {Value: 70.0, Present: true}},
			{{Value: 40.0, Present: true}, {Value: 90.0, Present: true}},
		},
	}
	comps := &types.CrosstabComponents{
		CellCounts: [][]int{{4, 4}, {2, 2}},
		CellComponents: [][]map[string]any{
			{{"n": 4, "sum": 80.0, "distinct_count": 2}, {"n": 4, "sum": 70.0, "distinct_count": 2}},
			{{"n": 2, "sum": 40.0, "distinct_count": 1}, {"n": 2, "sum": 90.0, "distinct_count": 1}},
		},
		// urban: 6 records, 3 respondents. rural: 4 records, 2.
		RowMarginCounts: []int{6, 4},
		RowMarginComponents: []map[string]any{
			{"n": 6, "sum": 120.0, "distinct_count": 3},
			{"n": 4, "sum": 130.0, "distinct_count": 2},
		},
		// acme: 101, 102, 103. zenith: 101, 104, 105.
		ColumnMarginCounts: []int{6, 6},
		ColumnMarginComponents: []map[string]any{
			{"n": 6, "sum": 120.0, "distinct_count": 3},
			{"n": 6, "sum": 160.0, "distinct_count": 3},
		},
	}
	return NewCrosstabHostViewWithComponents(mx, comps)
}

// TestPairwiseMarginDistinctN_ExactWhereCellSumOverCounts is the
// story's central evidence. The row margin's distinct count is the true
// respondent base; summing the row's per-cell distinct counts is not,
// because the fan-out column axis puts respondent 101 in both cells.
func TestPairwiseMarginDistinctN_ExactWhereCellSumOverCounts(t *testing.T) {
	host := pairwiseMarginDistinctHost()

	// The naive alternative, computed here rather than borrowed from
	// the implementation: sum each cell's distinct_count across the row.
	cellSum := func(rowIdx int) int {
		total := 0
		for c := range host.Payload().ColumnKeys {
			total += host.distinctCellN(rowIdx, c)
		}
		return total
	}

	got, ok := host.RowMarginDistinctN(0)
	if !ok {
		t.Fatal("RowMarginDistinctN(urban) not ok")
	}
	if got != 3 {
		t.Errorf("row_margin_distinct(urban) = %d, want 3 respondents", got)
	}
	if sum := cellSum(0); sum != 4 {
		t.Fatalf("fixture no longer over-counts: cell-sum(urban) = %d, want 4", sum)
	}
	if got == cellSum(0) {
		t.Error("margin and cell-sum agree; the fan-out column axis is not exercised")
	}

	// And it is not merely the record count either.
	records, ok := host.RowMarginN(0)
	if !ok {
		t.Fatal("RowMarginN(urban) not ok")
	}
	if records != 6 {
		t.Errorf("row_margin_n(urban) = %d, want 6 records", records)
	}
	if records == got {
		t.Error("record margin and distinct margin agree; the fixture no longer inflates")
	}

	// rural has no fan-out overlap, so margin and cell-sum coincide —
	// the gap is a property of the shape, not of the accessor.
	rural, ok := host.RowMarginDistinctN(1)
	if !ok {
		t.Fatal("RowMarginDistinctN(rural) not ok")
	}
	if rural != 2 || cellSum(1) != 2 {
		t.Errorf("rural: margin = %d, cell-sum = %d, want 2 and 2", rural, cellSum(1))
	}
}

// TestPairwiseMarginDistinctN_Accessors pins the nil-safe,
// bounds-checked posture the record-count twins already have, plus the
// key probe over BOTH admitted spellings.
func TestPairwiseMarginDistinctN_Accessors(t *testing.T) {
	host := pairwiseMarginDistinctHost()

	if n, ok := host.ColumnMarginDistinctN(1); !ok || n != 3 {
		t.Errorf("ColumnMarginDistinctN(zenith) = (%d, %v), want (3, true)", n, ok)
	}

	// AGG_DISTINCT_COUNT spells its figure "cardinality".
	cardHost := NewCrosstabHostViewWithComponents(
		&types.MatrixPayload{RowKeys: []types.AxisKey{{"r"}}, ColumnKeys: []types.AxisKey{{"c"}}},
		&types.CrosstabComponents{
			RowMarginComponents:    []map[string]any{{"n": 9, "cardinality": 4}},
			ColumnMarginComponents: []map[string]any{{"n": 9, "cardinality": 5}},
		})
	if n, ok := cardHost.RowMarginDistinctN(0); !ok || n != 4 {
		t.Errorf("RowMarginDistinctN over cardinality = (%d, %v), want (4, true)", n, ok)
	}
	if n, ok := cardHost.ColumnMarginDistinctN(0); !ok || n != 5 {
		t.Errorf("ColumnMarginDistinctN over cardinality = (%d, %v), want (5, true)", n, ok)
	}

	// Every unreadable shape must report ok=false. A zero n here is the
	// silently-wrong sample size this effort exists to remove.
	nilEntry := NewCrosstabHostViewWithComponents(
		&types.MatrixPayload{RowKeys: []types.AxisKey{{"r"}}, ColumnKeys: []types.AxisKey{{"c"}}},
		&types.CrosstabComponents{
			RowMarginComponents:    []map[string]any{nil},
			ColumnMarginComponents: []map[string]any{{"n": 3}}, // no distinct key at all
		})
	noComponents := NewCrosstabHostView(&types.MatrixPayload{RowKeys: []types.AxisKey{{"r"}}})
	nonNumeric := NewCrosstabHostViewWithComponents(
		&types.MatrixPayload{RowKeys: []types.AxisKey{{"r"}}, ColumnKeys: []types.AxisKey{{"c"}}},
		&types.CrosstabComponents{
			RowMarginComponents: []map[string]any{{"distinct_count": "three"}},
		})

	cases := []struct {
		name string
		get  func() (int, bool)
	}{
		{"nil margin entry", func() (int, bool) { return nilEntry.RowMarginDistinctN(0) }},
		{"entry without a distinct key", func() (int, bool) { return nilEntry.ColumnMarginDistinctN(0) }},
		{"index past the vector", func() (int, bool) { return host.RowMarginDistinctN(7) }},
		{"negative index", func() (int, bool) { return host.ColumnMarginDistinctN(-1) }},
		{"components disabled", func() (int, bool) { return noComponents.RowMarginDistinctN(0) }},
		{"nil host", func() (int, bool) { return (*CrosstabHostView)(nil).RowMarginDistinctN(0) }},
		{"non-numeric figure", func() (int, bool) { return nonNumeric.RowMarginDistinctN(0) }},
		{"absent margin vector", func() (int, bool) { return nonNumeric.ColumnMarginDistinctN(0) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n, ok := tc.get()
			if ok {
				t.Fatalf("expected ok=false, got (%d, true)", n)
			}
			if n != 0 {
				t.Fatalf("expected a zero value alongside ok=false, got %d", n)
			}
		})
	}
}

// TestOverlayPairwise_MarginDistinctLegsReachTheKernel drives the two
// modes through ApplyOverlays and checks the p-values against an
// independent pooled two-proportion z on the legs the margins name.
func TestOverlayPairwise_MarginDistinctLegsReachTheKernel(t *testing.T) {
	host := pairwiseMarginDistinctHost()

	run := func(nSource string) float64 {
		t.Helper()
		specs := []types.OverlaySpec{{
			Kind:   types.OverlayKindPairwisePropZ,
			Scope:  types.OverlayScopeRow,
			Params: json.RawMessage(`{"n_source":"` + nSource + `"}`),
		}}
		layers, _, err := ApplyOverlays(specs, host)
		if err != nil {
			t.Fatalf("ApplyOverlays(%s): %v", nSource, err)
		}
		// One pair (urban, rural); column 0 is `acme`.
		cell := layers[0].Payload.Matrix.Cells[0][0]
		if !cell.Present {
			t.Fatalf("pair cell absent under %s", nSource)
		}
		v, _ := cell.Value.(float64)
		return v
	}

	// p legs are the cell values /100: urban 0.80, rural 0.40.
	wantDistinct, ok := twoProportionZ(0.80*3, 3, 0.40*2, 2)
	if !ok {
		t.Fatal("twoProportionZ(distinct margin legs) undefined")
	}
	wantRecords, ok := twoProportionZ(0.80*6, 6, 0.40*4, 4)
	if !ok {
		t.Fatal("twoProportionZ(record margin legs) undefined")
	}

	gotDistinct := run(types.PairwiseNSourceRowMarginDistinct)
	gotRecords := run(types.PairwiseNSourceRowMarginN)
	if math.Abs(gotDistinct-wantDistinct) > 1e-12 {
		t.Errorf("row_margin_distinct p = %v, want %v (legs 3 and 2 respondents)", gotDistinct, wantDistinct)
	}
	if math.Abs(gotRecords-wantRecords) > 1e-12 {
		t.Errorf("row_margin_n p = %v, want %v (legs 6 and 4 records)", gotRecords, wantRecords)
	}
	if gotDistinct == gotRecords {
		t.Fatal("both margin modes produced the same p-value; the distinct leg is not reaching the margin")
	}

	// COLUMN scope pairs columns per row, so column_margin_distinct is
	// the leg each side reads. acme and zenith both hold 3 respondents
	// against 6 records, so the mode still has to read the right slot.
	colSpecs := []types.OverlaySpec{{
		Kind:   types.OverlayKindPairwisePropZ,
		Scope:  types.OverlayScopeColumn,
		Params: json.RawMessage(`{"n_source":"` + types.PairwiseNSourceColumnMarginDistinct + `"}`),
	}}
	layers, _, err := ApplyOverlays(colSpecs, host)
	if err != nil {
		t.Fatalf("ApplyOverlays(column_margin_distinct): %v", err)
	}
	// Row 0 (urban), pair 0 (acme, zenith): 0.80 vs 0.70 at n = 3, 3.
	cell := layers[0].Payload.Matrix.Cells[0][0]
	if !cell.Present {
		t.Fatal("column-scope pair cell absent")
	}
	wantCol, ok := twoProportionZ(0.80*3, 3, 0.70*3, 3)
	if !ok {
		t.Fatal("twoProportionZ(column margin legs) undefined")
	}
	if v, _ := cell.Value.(float64); math.Abs(v-wantCol) > 1e-12 {
		t.Errorf("column_margin_distinct p = %v, want %v", v, wantCol)
	}
}

// TestOverlayPairwise_MarginDistinctMissingEntrySkips pins the
// missing-leg rule: a host with NO margin components (the display flag
// was off, or components were built without margins) must skip every
// pair with the aggregated warning, never compute against n=0.
func TestOverlayPairwise_MarginDistinctMissingEntrySkips(t *testing.T) {
	full := pairwiseMarginDistinctHost()
	stripped := NewCrosstabHostViewWithComponents(full.Payload(), &types.CrosstabComponents{
		CellCounts:     full.Components().CellCounts,
		CellComponents: full.Components().CellComponents,
		// RowMarginComponents / ColumnMarginComponents deliberately absent.
	})

	for _, nSource := range []string{
		types.PairwiseNSourceRowMarginDistinct,
		types.PairwiseNSourceColumnMarginDistinct,
	} {
		t.Run(nSource, func(t *testing.T) {
			specs := []types.OverlaySpec{{
				Kind:   types.OverlayKindPairwisePropZ,
				Scope:  types.OverlayScopeRow,
				Params: json.RawMessage(`{"n_source":"` + nSource + `"}`),
			}}
			layers, warns, err := ApplyOverlays(specs, stripped)
			if err != nil {
				t.Fatalf("ApplyOverlays: %v", err)
			}
			if len(layers) != 1 {
				t.Fatalf("expected 1 layer, got %d", len(layers))
			}
			for r, row := range layers[0].Payload.Matrix.Cells {
				for c, cell := range row {
					if cell.Present {
						t.Fatalf("cell (%d,%d) present with an unreadable margin leg: %v", r, c, cell.Value)
					}
				}
			}
			if len(warns) == 0 {
				t.Fatal("an unreadable margin leg must produce the per-pair skip warning, not silence")
			}
			found := false
			for _, w := range warns {
				if w.Code == string(errors.PULSE_OVERLAY_REF_ZERO) {
					found = true
				}
			}
			if !found {
				t.Fatalf("warnings %+v carry no PULSE_OVERLAY_REF_ZERO skip", warns)
			}
		})
	}
}

// TestOverlayPairwise_MarginDistinctSharesTheAdmissionGate proves the
// E1-S2 cell-aggregator admission covers the margin modes too. An
// AGG_FREQUENCY cell spells a key "distinct_count" that counts distinct
// VALUES of the measure field; its MARGIN slot spells it the same way,
// so a key-presence read would report answer codes as a respondent base.
func TestOverlayPairwise_MarginDistinctSharesTheAdmissionGate(t *testing.T) {
	freq := NewCrosstabHostViewWithComponents(
		&types.MatrixPayload{
			RowHeader:  types.AxisHeader{Fields: []string{"segment"}},
			RowKeys:    []types.AxisKey{{"urban"}, {"rural"}},
			ColumnKeys: []types.AxisKey{{"acme"}},
			Cells: [][]types.MatrixCell{
				{{Value: 80.0, Present: true}},
				{{Value: 40.0, Present: true}},
			},
		},
		&types.CrosstabComponents{
			CellComponents: [][]map[string]any{
				{{"n": 4, "distinct_count": 9, "mode_value": "a", "mode_count": 3}},
				{{"n": 2, "distinct_count": 7, "mode_value": "b", "mode_count": 2}},
			},
			RowMarginComponents: []map[string]any{
				{"n": 6, "distinct_count": 9, "mode_value": "a", "mode_count": 4},
				{"n": 4, "distinct_count": 7, "mode_value": "b", "mode_count": 3},
			},
		})

	for _, nSource := range []string{
		types.PairwiseNSourceRowMarginDistinct,
		types.PairwiseNSourceColumnMarginDistinct,
	} {
		t.Run(nSource, func(t *testing.T) {
			specs := []types.OverlaySpec{{
				Kind:   types.OverlayKindPairwisePropZ,
				Scope:  types.OverlayScopeRow,
				Params: json.RawMessage(`{"n_source":"` + nSource + `"}`),
			}}
			_, _, err := ApplyOverlays(specs, freq)
			if err == nil {
				t.Fatal("expected an AGG_FREQUENCY cell host to be refused")
			}
			if !pairwiseErrHasCode(err, errors.PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE) {
				t.Fatalf("error %v does not carry PULSE_OVERLAY_REF_INCOMPATIBLE_WITH_SHAPE", err)
			}
			// The refusal must name the mode the caller actually asked
			// for, not the first distinct mode that shipped.
			if !strings.Contains(err.Error(), "n_source="+nSource) {
				t.Errorf("refusal %q does not name n_source=%s", err.Error(), nSource)
			}
			if !strings.Contains(err.Error(), string(types.AGG_FREQUENCY)) {
				t.Errorf("refusal %q does not name the observed aggregator", err.Error())
			}
		})
	}

	// The admitted host runs — the gate is not refusing everything.
	specs := []types.OverlaySpec{{
		Kind:   types.OverlayKindPairwisePropZ,
		Scope:  types.OverlayScopeRow,
		Params: json.RawMessage(`{"n_source":"` + types.PairwiseNSourceRowMarginDistinct + `"}`),
	}}
	if _, _, err := ApplyOverlays(specs, pairwiseMarginDistinctHost()); err != nil {
		t.Fatalf("admitted AGG_DISTINCT_SUM host refused: %v", err)
	}
}
