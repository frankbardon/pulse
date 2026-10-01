package pulse

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// End-to-end coverage for n_source=row_margin_distinct /
// column_margin_distinct over the SAME memmap cohort E1-S2 writes, with
// the multi-response level moved to the COLUMN axis:
//
//	rows    = GROUP_CATEGORY on `segment`
//	columns = GROUP_SET_PER_ELEMENT on the multi-select `brand`
//
// That shape is what separates a margin from a cell-sum. Respondent 101
// selected BOTH brands, so the `urban` row's two cells hold two
// respondents each while the row itself holds three:
//
//	cell (urban, acme)   -> 101, 102          distinct 2   records 4
//	cell (urban, zenith) -> 101, 104          distinct 2   records 4
//	row  urban           -> 101, 102, 104     distinct 3   records 6
//
// Summing the cells says 4. The margin says 3, and the margin is right,
// because it recomputes the cell aggregator over the raw records that
// reached the row key — once each, whatever the column axis does. There
// is no summing step for the fan-out to double-count through, which is
// exactly why these two modes are NOT subject to the E1-S3 partition
// gate.

// pwMarginDistinctRequest is the column-fan-out crosstab over the E1-S2
// cohort. nSource empty leaves the overlay off entirely.
func pwMarginDistinctRequest(nSource string) *Request {
	req := &Request{
		Cohort: &types.Cohort{Filename: pwDistinctCohort},
		Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "segment"}},
			Columns: []*types.Group{{Type: types.GROUP_SET_PER_ELEMENT, Field: "brand"}},
			Cell: &types.Aggregation{
				Type:   types.AGG_DISTINCT_SUM,
				Field:  "value",
				Label:  "distinct_value",
				Params: json.RawMessage(`{"distinct_by":"respondent"}`),
			},
			Shape:   types.CrosstabShapeMatrix,
			Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		},
	}
	if nSource != "" {
		req.Overlays = []types.OverlaySpec{{
			Name:   "pw",
			Kind:   types.OverlayKindPairwisePropZ,
			Scope:  types.OverlayScopeRow,
			Params: json.RawMessage(`{"n_source":"` + nSource + `"}`),
		}}
	}
	return req
}

func pwMarginAxisIndex(t *testing.T, keys []types.AxisKey, want string) int {
	t.Helper()
	for i, k := range keys {
		if len(k) == 1 && k[0] == want {
			return i
		}
	}
	t.Fatalf("axis key %q not found in %v", want, keys)
	return -1
}

// TestPairwiseMarginDistinct_MemmapExactWhereCellSumOverCounts is the
// story's main evidence, on a real cohort rather than a hand-built
// components block: the distinct row margin is the true respondent
// count, and the cell-sum over the same row is not.
func TestPairwiseMarginDistinct_MemmapExactWhereCellSumOverCounts(t *testing.T) {
	memFs := afero.NewMemMapFs()
	writePairwiseDistinctCohort(t, memFs)

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := p.Process(context.Background(), pwMarginDistinctRequest(""))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if resp.Crosstab == nil || resp.Crosstab.Matrix == nil {
		t.Fatal("expected a MATRIX crosstab payload")
	}
	if resp.Components == nil || resp.Components.Crosstab == nil {
		t.Fatal("expected Response.Components.Crosstab")
	}
	mx := resp.Crosstab.Matrix
	// A one-slot compose view builds the same components-bearing MATRIX
	// host the crosstab overlay fold does.
	host := processing.NewComposeHostView([]*types.Response{resp}).Slot(0).Matrix()

	if agg, key, ok := host.AdmitsDistinctKeyN(); !ok || agg != types.AGG_DISTINCT_SUM || key != "distinct_count" {
		t.Fatalf("AdmitsDistinctKeyN = (%q, %q, %v), want (AGG_DISTINCT_SUM, distinct_count, true)", agg, key, ok)
	}

	urban := pwMarginAxisIndex(t, mx.RowKeys, "urban")
	rural := pwMarginAxisIndex(t, mx.RowKeys, "rural")
	acme := pwMarginAxisIndex(t, mx.ColumnKeys, "acme")
	zenith := pwMarginAxisIndex(t, mx.ColumnKeys, "zenith")

	// The naive alternative, read straight off the components block so
	// the assertion does not borrow the accessor it is testing.
	cellDistinct := func(r, c int) int {
		m := resp.Components.Crosstab.CellComponents[r][c]
		f, ok := m["distinct_count"]
		if !ok {
			t.Fatalf("cell (%d,%d) carries no distinct_count: %v", r, c, m)
		}
		switch x := f.(type) {
		case int:
			return x
		case float64:
			return int(x)
		}
		t.Fatalf("cell (%d,%d) distinct_count is %T", r, c, f)
		return 0
	}

	sum := cellDistinct(urban, acme) + cellDistinct(urban, zenith)
	if sum != 4 {
		t.Fatalf("cell-sum over the urban row = %d, want 4 (the over-count the margin removes)", sum)
	}

	gotDistinct, ok := host.RowMarginDistinctN(urban)
	if !ok {
		t.Fatal("RowMarginDistinctN(urban) not ok")
	}
	if gotDistinct != 3 {
		t.Errorf("row_margin_distinct(urban) = %d, want 3 respondents (101, 102, 104)", gotDistinct)
	}
	if gotDistinct == sum {
		t.Error("margin equals the cell-sum; the multi-response column axis is not fanning out")
	}

	gotRecords, ok := host.RowMarginN(urban)
	if !ok {
		t.Fatal("RowMarginN(urban) not ok")
	}
	if gotRecords != 6 {
		t.Errorf("row_margin_n(urban) = %d, want 6 records", gotRecords)
	}
	if gotRecords == gotDistinct {
		t.Error("record margin and distinct margin agree; the fixture no longer inflates")
	}

	// rural: 103 and 105, one brand each, so nothing over-counts there.
	if n, ok := host.RowMarginDistinctN(rural); !ok || n != 2 {
		t.Errorf("row_margin_distinct(rural) = (%d, %v), want (2, true)", n, ok)
	}

	// Column margins are distinct too: acme = 101, 102, 103.
	if n, ok := host.ColumnMarginDistinctN(acme); !ok || n != 3 {
		t.Errorf("column_margin_distinct(acme) = (%d, %v), want (3, true)", n, ok)
	}
	if n, ok := host.ColumnMarginN(acme); !ok || n != 6 {
		t.Errorf("column_margin_n(acme) = (%d, %v), want (6, true)", n, ok)
	}
}

// TestPairwiseMarginDistinct_MemmapPValues drives the overlay both ways
// through the public facade and checks each p-value against the
// independent pooled two-proportion z written for E1-S2.
func TestPairwiseMarginDistinct_MemmapPValues(t *testing.T) {
	memFs := afero.NewMemMapFs()
	writePairwiseDistinctCohort(t, memFs)

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	pairP := func(nSource string) float64 {
		t.Helper()
		resp, perr := p.Process(context.Background(), pwMarginDistinctRequest(nSource))
		if perr != nil {
			t.Fatalf("Process(%s): %v", nSource, perr)
		}
		if len(resp.Overlays) != 1 {
			t.Fatalf("Process(%s): expected 1 overlay layer, got %d", nSource, len(resp.Overlays))
		}
		omx := resp.Overlays[0].Payload.Matrix
		if omx == nil {
			t.Fatalf("Process(%s): nil overlay matrix", nSource)
		}
		// One pair (the two segments) × the brand columns; column
		// order is the host's own.
		acme := pwMarginAxisIndex(t, resp.Crosstab.Matrix.ColumnKeys, "acme")
		if len(omx.RowKeys) != 1 {
			t.Fatalf("Process(%s): expected 1 pair, got %d", nSource, len(omx.RowKeys))
		}
		cell := omx.Cells[0][acme]
		if !cell.Present {
			t.Fatalf("Process(%s): pair cell at acme absent", nSource)
		}
		v, _ := cell.Value.(float64)
		return v
	}

	// Leg order follows the host row order; find which way round it is
	// so the oracle pairs the same legs the overlay did.
	resp, err := p.Process(context.Background(), pwMarginDistinctRequest(""))
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	mx := resp.Crosstab.Matrix
	first, second := mx.RowKeys[0][0].(string), mx.RowKeys[1][0].(string)

	// (urban, acme) sums the first value per distinct respondent:
	// 30 + 50 = 80. (rural, acme) is 103 alone: 40.
	props := map[string]float64{"urban": 0.80, "rural": 0.40}
	distinctN := map[string]int{"urban": 3, "rural": 2}
	recordN := map[string]int{"urban": 6, "rural": 4}

	wantDistinct := pwPooledPropZ(props[first], distinctN[first], props[second], distinctN[second])
	wantRecords := pwPooledPropZ(props[first], recordN[first], props[second], recordN[second])

	gotDistinct := pairP(types.PairwiseNSourceRowMarginDistinct)
	gotRecords := pairP(types.PairwiseNSourceRowMarginN)

	if math.Abs(gotDistinct-wantDistinct) > 1e-9 {
		t.Errorf("row_margin_distinct p = %v, want %v (legs 3 and 2 respondents)", gotDistinct, wantDistinct)
	}
	if math.Abs(gotRecords-wantRecords) > 1e-9 {
		t.Errorf("row_margin_n p = %v, want %v (legs 6 and 4 records)", gotRecords, wantRecords)
	}
	if math.Abs(gotDistinct-gotRecords) < 1e-9 {
		t.Fatal("both margin modes produced the same p-value; the distinct leg is not reaching the margin")
	}

	// column_margin_distinct reads the same figure off the column
	// vector: acme and zenith both hold 3 respondents against 6
	// records, so the two n legs are equal and the p-value differs
	// from the record-count reading.
	gotCol := pairP(types.PairwiseNSourceColumnMarginDistinct)
	wantCol := pwPooledPropZ(props[first], 3, props[second], 3)
	if math.Abs(gotCol-wantCol) > 1e-9 {
		t.Errorf("column_margin_distinct p = %v, want %v (both legs 3 respondents)", gotCol, wantCol)
	}
}

// TestPairwiseMarginDistinct_MemmapFanOutInnerLevelRunsClean is the
// false-refusal guard the acceptance criterion names explicitly: the
// OFFENDING axis shape for n_within_distinct — a fan-out level the slab
// would sum across — must run clean under row_margin_distinct at BOTH
// predict and process, because a margin does not sum cells.
func TestPairwiseMarginDistinct_MemmapFanOutInnerLevelRunsClean(t *testing.T) {
	memFs := afero.NewMemMapFs()
	writePairwiseDistinctCohort(t, memFs)

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Sanity: the very same shape IS refused under n_within_distinct.
	if _, perr := p.Process(context.Background(),
		pwPartitionRequest(types.PairwiseNSourceNWithinDistinct, 0)); perr == nil {
		t.Fatal("n_within_distinct over the fan-out inner level must still refuse")
	}

	req := pwPartitionRequest(types.PairwiseNSourceRowMarginDistinct, 0)
	env := pwPartitionPredictEnvelope(t, memFs, req)
	if e := pwPartitionEnvelopeCode(env, string(errors.PULSE_OVERLAY_DISTINCT_SLAB_NOT_PARTITIONED)); e != nil {
		t.Fatalf("predict gated row_margin_distinct: %s", e.Message)
	}
	// And predict must not refuse the mode as UNKNOWN either — a
	// constant that never reaches ValidPairwiseNSource is a mode no
	// request can name, whatever the runtime switch does with it.
	if e := pwPartitionEnvelopeCode(env, string(errors.PULSE_OVERLAY_PARAM_MISSING)); e != nil {
		t.Fatalf("predict rejected row_margin_distinct as a bad param: %s", e.Message)
	}
	resp, perr := p.Process(context.Background(), req)
	if perr != nil {
		t.Fatalf("Process gated row_margin_distinct over a fan-out inner level: %v", perr)
	}
	if len(resp.Overlays) != 1 {
		t.Fatalf("expected 1 overlay layer, got %d", len(resp.Overlays))
	}
	present := 0
	for _, row := range resp.Overlays[0].Payload.Matrix.Cells {
		for _, cell := range row {
			if cell.Present {
				present++
			}
		}
	}
	if present == 0 {
		t.Fatal("layer ran but every cell is absent — the margin leg is not readable")
	}
}

// TestPairwiseMarginDistinct_MemmapMissingMarginSkips pins the
// missing-leg rule end to end. Turning the row-margin DISPLAY flag off
// suppresses RowMarginComponents (the display-flag gate in
// populateCrosstabComponents), so every pair must skip with the
// aggregated warning. A zero n would render a p-value off a sample size
// that was never measured.
func TestPairwiseMarginDistinct_MemmapMissingMarginSkips(t *testing.T) {
	memFs := afero.NewMemMapFs()
	writePairwiseDistinctCohort(t, memFs)

	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req := pwMarginDistinctRequest(types.PairwiseNSourceRowMarginDistinct)
	req.Crosstab.Margins.Rows = false

	resp, perr := p.Process(context.Background(), req)
	if perr != nil {
		t.Fatalf("Process: %v", perr)
	}
	if resp.Components.Crosstab.RowMarginComponents != nil {
		t.Fatal("fixture invalid: the display-flag gate no longer suppresses RowMarginComponents")
	}
	if len(resp.Overlays) != 1 {
		t.Fatalf("expected 1 overlay layer, got %d", len(resp.Overlays))
	}
	for r, row := range resp.Overlays[0].Payload.Matrix.Cells {
		for c, cell := range row {
			if cell.Present {
				t.Fatalf("cell (%d,%d) present with no margin components: %v", r, c, cell.Value)
			}
		}
	}
	found := false
	for _, w := range resp.Warnings {
		if w.Code == string(errors.PULSE_OVERLAY_REF_ZERO) {
			found = true
		}
	}
	if !found {
		t.Fatalf("no PULSE_OVERLAY_REF_ZERO skip warning on the response: %+v", resp.Warnings)
	}
}
