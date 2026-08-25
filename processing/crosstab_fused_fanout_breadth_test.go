package processing

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// E2-S4 — breadth of the fan-out equivalence harness.
//
// The E2-S3 matrix (crosstab_fused_fanout_test.go) covers the routing
// spine: one fan per axis, a fan behind and ahead of a single-key
// position, Include on the ROW axis, and the three normalization modes
// one at a time. This file widens that surface along the axes E2-S3
// explicitly left open — Include on a COLUMN fan, three-position axes,
// two fans on ONE axis, normalize_level composed WITH normalize_within,
// the cell-aggregator families beyond SUM/COUNT/AVERAGE, overlays over
// a fanning host, and the DisableComponents wire shape.
//
// Every case funnels through assertFusedBufferedParity, which asserts
// CanFuseCrosstab admits the request FIRST — a parity assertion over a
// request the gate rejects would compare two buffered runs and pass
// while proving nothing.

// includeSetGroup is setGroup with an ordered inclusion list. Include
// ordering is order-sensitive end to end (orderKeysByInclude), so the
// helper takes the labels in author order.
func includeSetGroup(field string, include ...string) *types.Group {
	return &types.Group{Type: types.GROUP_SET_PER_ELEMENT, Field: field, Include: include}
}

// TestFusedCrosstab_FanOutBreadthMatchesBuffered extends the E2-S3
// oracle matrix with the shapes that matrix does not reach. Buffered
// remains the oracle: the contract is byte-equality, not a
// hand-computed number.
func TestFusedCrosstab_FanOutBreadthMatchesBuffered(t *testing.T) {
	zero, one := 0, 1
	cases := []struct {
		name string
		spec *types.CrosstabSpec
	}{
		{
			// Two fanning positions on ONE axis: the per-position key
			// sets multiply within the axis before the row x column
			// product multiplies across axes. A record selecting 3 tags
			// and 2 channels produces 6 row keys here, not 6 cells.
			name: "two fanning positions on the same axis",
			spec: &types.CrosstabSpec{
				Rows:    []*types.Group{setGroup("tags"), setGroup("chans")},
				Columns: []*types.Group{catGroup("region")},
				Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "sum"},
				Shape:   types.CrosstabShapeMatrix,
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
		},
		{
			// Three positions, fan in the middle: the leading key is
			// shared by every fanned tuple and the trailing position
			// keys independently behind it.
			name: "three-position row axis with the fan in the middle",
			spec: &types.CrosstabSpec{
				Rows:    []*types.Group{catGroup("region"), setGroup("tags"), rangeGroup("value")},
				Columns: []*types.Group{catGroup("region")},
				Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "region", Label: "n"},
				Shape:   types.CrosstabShapeMatrix,
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
		},
		{
			// Three positions with TWO fans behind a single-key lead.
			name: "three-position row axis with two fans behind a single key",
			spec: &types.CrosstabSpec{
				Rows:    []*types.Group{catGroup("region"), setGroup("chans"), setGroup("tags")},
				Columns: []*types.Group{catGroup("region")},
				Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "sum"},
				Shape:   types.CrosstabShapeMatrix,
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
		},
		{
			// The same three-position shape on the COLUMN axis, fanning
			// at position 0 so the column tuple order is exercised.
			name: "three-position column axis fanning at position zero",
			spec: &types.CrosstabSpec{
				Rows:    []*types.Group{catGroup("region")},
				Columns: []*types.Group{setGroup("tags"), catGroup("region"), rangeGroup("value")},
				Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "region", Label: "n"},
				Shape:   types.CrosstabShapeMatrix,
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
		},
		{
			// Include on a COLUMN fanning axis — E2-S3 covered the row
			// side only. Column-key ordering, the column margin vector
			// and column_key_components all key off the include order.
			name: "Include on a column fanning axis",
			spec: &types.CrosstabSpec{
				Rows:    []*types.Group{catGroup("region")},
				Columns: []*types.Group{includeSetGroup("chans", "ATM", "WEB")},
				Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "sum"},
				Shape:   types.CrosstabShapeMatrix,
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
		},
		{
			// Include on BOTH fanning axes at once, each in a
			// non-dictionary order.
			name: "Include on both fanning axes",
			spec: &types.CrosstabSpec{
				Rows:    []*types.Group{includeSetGroup("tags", "DISC", "VISA")},
				Columns: []*types.Group{includeSetGroup("chans", "ATM", "WEB")},
				Cell:    &types.Aggregation{Type: types.AGG_AVERAGE, Field: "value", Label: "avg"},
				Shape:   types.CrosstabShapeMatrix,
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
		},
		{
			// Include on a fanning position that sits BEHIND a
			// single-key one, so the filtered fan interacts with a
			// shared prefix.
			name: "Include on a fanning position behind a single key",
			spec: &types.CrosstabSpec{
				Rows:    []*types.Group{catGroup("region"), includeSetGroup("tags", "AMEX", "MC")},
				Columns: []*types.Group{catGroup("region")},
				Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "region", Label: "n"},
				Shape:   types.CrosstabShapeMatrix,
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
		},
		{
			// Plain normalize=column over a fanning COLUMN axis — the
			// E2-S3 matrix reached column normalization only in
			// combination with normalize_within.
			name: "normalize column over a fanning column axis",
			spec: &types.CrosstabSpec{
				Rows:      []*types.Group{setGroup("tags")},
				Columns:   []*types.Group{setGroup("chans")},
				Cell:      &types.Aggregation{Type: types.AGG_COUNT, Field: "region", Label: "n"},
				Shape:     types.CrosstabShapeMatrix,
				Normalize: types.CrosstabNormalizeColumn,
				Margins:   types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
		},
		{
			// normalize=total over fanning axes: the denominator is the
			// grand margin, which counts each record ONCE even though
			// the cells it feeds count it many times. The normalized
			// cells therefore need not sum to 1 — whatever buffered
			// produces is the contract.
			name: "normalize total over both fanning axes",
			spec: &types.CrosstabSpec{
				Rows:      []*types.Group{setGroup("tags")},
				Columns:   []*types.Group{setGroup("chans")},
				Cell:      &types.Aggregation{Type: types.AGG_COUNT, Field: "region", Label: "n"},
				Shape:     types.CrosstabShapeMatrix,
				Normalize: types.CrosstabNormalizeTotal,
				Margins:   types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
		},
		{
			// normalize_level AND normalize_within on the same request,
			// with a fan on each axis: the level truncates the row
			// denominator to its prefix while the within-prefix scopes
			// the column side. The two compose, and both read fanned
			// axis state.
			name: "normalize_level and normalize_within composed",
			spec: &types.CrosstabSpec{
				Rows:            []*types.Group{catGroup("region"), setGroup("tags")},
				Columns:         []*types.Group{catGroup("region"), setGroup("chans")},
				Cell:            &types.Aggregation{Type: types.AGG_COUNT, Field: "region", Label: "n"},
				Shape:           types.CrosstabShapeMatrix,
				Normalize:       types.CrosstabNormalizeRow,
				NormalizeLevel:  &zero,
				NormalizeWithin: &zero,
				Margins:         types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
		},
		{
			// normalize_level pointing at the DEEPER of two positions
			// on a three-position axis whose middle position fans.
			name: "normalize_level at the fanning position of a three-position axis",
			spec: &types.CrosstabSpec{
				Rows:           []*types.Group{catGroup("region"), setGroup("tags"), rangeGroup("value")},
				Columns:        []*types.Group{catGroup("region")},
				Cell:           &types.Aggregation{Type: types.AGG_COUNT, Field: "region", Label: "n"},
				Shape:          types.CrosstabShapeMatrix,
				Normalize:      types.CrosstabNormalizeRow,
				NormalizeLevel: &one,
				Margins:        types.CrosstabMargins{Rows: true},
			},
		},
		{
			// Long shape with Include on a fanning column axis: the
			// long emitter walks the same finalised keys in the same
			// order, so include ordering has to survive the reshape.
			name: "long shape with Include on a fanning column axis",
			spec: &types.CrosstabSpec{
				Rows:    []*types.Group{catGroup("region")},
				Columns: []*types.Group{includeSetGroup("chans", "POS", "WEB")},
				Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "sum"},
				Shape:   types.CrosstabShapeLong,
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
		},
		{
			// No margins at all with both axes fanning: the margin
			// vectors must be absent on both paths, not merely equal in
			// value (omitempty makes this a wire-shape assertion).
			name: "no margins with both axes fanning",
			spec: &types.CrosstabSpec{
				Rows:    []*types.Group{setGroup("tags")},
				Columns: []*types.Group{setGroup("chans")},
				Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "sum"},
				Shape:   types.CrosstabShapeMatrix,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schema := fanoutCrosstabSchema(t)
			recs := fanoutRecords(schema)
			assertFusedBufferedParity(t, schema, &types.Request{Crosstab: tc.spec}, recs)
		})
	}
}

// TestFusedCrosstab_FanOutCellAggregatorBreadth walks the cell
// aggregators the fusion gate admits beyond the SUM / COUNT / AVERAGE
// trio the E2-S3 matrix uses. Admission = Mergeable() AND a
// MarginReducibility of MarginSummable or MarginMeanReducible, so the
// families here are the counting ones, the mean-reducible ones, and the
// set-family ones — including two that fold a SET field inside a cell
// whose axis is itself fanning on a set field.
func TestFusedCrosstab_FanOutCellAggregatorBreadth(t *testing.T) {
	cases := []struct {
		name string
		cell *types.Aggregation
		// recs overrides the shared fixture for aggregators whose
		// arithmetic is undefined over it (AGG_RATIO divides by a
		// per-cell denominator sum, which the null-"value" fixture row
		// can drive to zero — a NaN that is path-independent and has
		// nothing to do with fan-out).
		recs func(schema *encoding.Schema) []*Record
	}{
		{
			name: "AGG_NULL_COUNT",
			cell: &types.Aggregation{Type: types.AGG_NULL_COUNT, Field: "value", Label: "nulls"},
		},
		{
			name: "AGG_DISTINCT_COUNT",
			cell: &types.Aggregation{Type: types.AGG_DISTINCT_COUNT, Field: "value", Label: "distinct"},
		},
		{
			name: "AGG_FREQUENCY",
			cell: &types.Aggregation{Type: types.AGG_FREQUENCY, Field: "region", Label: "freq"},
		},
		{
			name: "AGG_WEIGHTED_MEAN",
			cell: &types.Aggregation{
				Type:   types.AGG_WEIGHTED_MEAN,
				Field:  "region",
				Label:  "wmean",
				Params: json.RawMessage(`{"weight_field":"value"}`),
			},
		},
		{
			name: "AGG_RATIO",
			cell: &types.Aggregation{
				Type:   types.AGG_RATIO,
				Field:  "value",
				Label:  "ratio",
				Params: json.RawMessage(`{"numerator_field":"region","denominator_field":"value"}`),
			},
			recs: nonNullValueFanoutRecords,
		},
		{
			// A set-family cell aggregator over the OTHER set field
			// while the row axis fans on "tags": the cell folds a mask
			// per contributing record, once per fanned bucket.
			name: "AGG_SET_CARDINALITY_SUM",
			cell: &types.Aggregation{Type: types.AGG_SET_CARDINALITY_SUM, Field: "chans", Label: "popcount"},
		},
		{
			name: "AGG_SET_CARDINALITY_AVG",
			cell: &types.Aggregation{Type: types.AGG_SET_CARDINALITY_AVG, Field: "chans", Label: "avg_popcount"},
		},
		{
			name: "AGG_SET_UNION",
			cell: &types.Aggregation{Type: types.AGG_SET_UNION, Field: "chans", Label: "union"},
		},
		{
			// Map-valued rich payload. Legal without normalization;
			// PULSE_CROSSTAB_NORMALIZE_MAP_VALUED covers the other case.
			name: "AGG_SET_FREQUENCY",
			cell: &types.Aggregation{Type: types.AGG_SET_FREQUENCY, Field: "chans", Label: "set_freq"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schema := fanoutCrosstabSchema(t)
			build := tc.recs
			if build == nil {
				build = fanoutRecords
			}
			recs := build(schema)
			req := &types.Request{Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{setGroup("tags")},
				Columns: []*types.Group{catGroup("region")},
				Cell:    tc.cell,
				Shape:   types.CrosstabShapeMatrix,
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			}}
			assertFusedBufferedParity(t, schema, req, recs)
		})
	}
}

// nonNullValueFanoutRecords is fanoutRecords with the null-"value" row
// dropped and the fan-out shape otherwise intact (multi-label rows,
// single-label rows, an empty mask, a null set). Used by cell
// aggregators that divide by a per-cell sum over "value".
func nonNullValueFanoutRecords(schema *encoding.Schema) []*Record {
	rows := []fanoutRow{
		{region: 0, tags: tagVISA | tagMC | tagAMEX, chans: chWEB | chPOS, value: 10},
		{region: 0, tags: tagVISA, chans: chWEB, value: 20},
		{region: 1, tags: 0, chans: chATM, value: 30},
		{region: 1, tags: 0, chans: chPOS, value: 40, nullTags: true},
		{region: 1, tags: tagDISC, chans: 0, value: 50, nullChans: true},
		{region: 0, tags: tagAMEX | tagDISC, chans: chWEB | chPOS | chATM, value: 7},
		{region: 1, tags: tagMC | tagDISC, chans: chWEB | chATM, value: 15},
	}
	out := make([]*Record, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.build(schema))
	}
	return out
}

// TestFusedCrosstab_FanOutWithOverlaysMatchesBuffered is the
// combination this effort exists for: E1 stopped overlays forcing the
// buffered path, E2 put fan-out axes on the fused path, and nothing so
// far ran the two together. The overlay fold reads the finalised
// matrix AND Response.Components.Crosstab, both of which fan-out
// reshapes, so a routing divergence surfaces here as a wrong overlay
// value even when the base cells agree.
func TestFusedCrosstab_FanOutWithOverlaysMatchesBuffered(t *testing.T) {
	cases := []struct {
		name     string
		spec     *types.CrosstabSpec
		overlays []types.OverlaySpec
	}{
		{
			name: "index_vs_margin row over a fanning row axis",
			spec: &types.CrosstabSpec{
				Rows:    []*types.Group{setGroup("tags")},
				Columns: []*types.Group{catGroup("region")},
				Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "sum"},
				Shape:   types.CrosstabShapeMatrix,
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
			overlays: []types.OverlaySpec{{
				Name:  "row_index",
				Kind:  types.OverlayKindIndexVsMargin,
				Scope: types.OverlayScopeCell,
				Ref:   types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}},
			}},
		},
		{
			name: "index_vs_margin column over a fanning column axis",
			spec: &types.CrosstabSpec{
				Rows:    []*types.Group{catGroup("region")},
				Columns: []*types.Group{setGroup("chans")},
				Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "sum"},
				Shape:   types.CrosstabShapeMatrix,
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
			overlays: []types.OverlaySpec{{
				Name:  "col_index",
				Kind:  types.OverlayKindIndexVsMargin,
				Scope: types.OverlayScopeCell,
				Ref:   types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisColumn}},
			}},
		},
		{
			// share_of_row divides by a row margin that, on a fanning
			// axis, counts the record once per label — the exact place
			// the non-additivity wart could leak into an overlay.
			name: "share_of_row with both axes fanning",
			spec: &types.CrosstabSpec{
				Rows:    []*types.Group{setGroup("tags")},
				Columns: []*types.Group{setGroup("chans")},
				Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "sum"},
				Shape:   types.CrosstabShapeMatrix,
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
			overlays: []types.OverlaySpec{{
				Name:  "share",
				Kind:  types.OverlayKindShareOfRow,
				Scope: types.OverlayScopeCell,
				Ref:   types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}},
			}},
		},
		{
			// PAIRWISE_PROP_Z is the components-READING overlay: it
			// reaches into Components.Crosstab per-cell n and margin
			// counts rather than the MatrixPayload alone.
			name: "pairwise_prop_z over a fanning row axis",
			spec: &types.CrosstabSpec{
				Rows:    []*types.Group{setGroup("tags")},
				Columns: []*types.Group{catGroup("region")},
				Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "region", Label: "n"},
				Shape:   types.CrosstabShapeMatrix,
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
			overlays: []types.OverlaySpec{{
				Name:  "pz",
				Kind:  types.OverlayKindPairwisePropZ,
				Scope: types.OverlayScopeRow,
			}},
		},
		{
			// Two layers over a composed fanning axis: layer order is
			// part of the wire shape.
			name: "two layers over a composed fanning row axis",
			spec: &types.CrosstabSpec{
				Rows:    []*types.Group{catGroup("region"), setGroup("tags")},
				Columns: []*types.Group{setGroup("chans")},
				Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "sum"},
				Shape:   types.CrosstabShapeMatrix,
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
			overlays: []types.OverlaySpec{
				{
					Name:  "row_index",
					Kind:  types.OverlayKindIndexVsMargin,
					Scope: types.OverlayScopeCell,
					Ref:   types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}},
				},
				{
					Name:  "col_index",
					Kind:  types.OverlayKindIndexVsMargin,
					Scope: types.OverlayScopeCell,
					Ref:   types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisColumn}},
				},
			},
		},
		{
			// Include-filtered fan under an overlay: the overlay
			// coordinates key off the include-ordered axis keys.
			name: "index_vs_margin over an Include-filtered fan",
			spec: &types.CrosstabSpec{
				Rows:    []*types.Group{includeSetGroup("tags", "DISC", "VISA")},
				Columns: []*types.Group{catGroup("region")},
				Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "sum"},
				Shape:   types.CrosstabShapeMatrix,
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
			overlays: []types.OverlaySpec{{
				Name:  "row_index",
				Kind:  types.OverlayKindIndexVsMargin,
				Scope: types.OverlayScopeCell,
				Ref:   types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}},
			}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schema := fanoutCrosstabSchema(t)
			recs := fanoutRecords(schema)
			req := &types.Request{Crosstab: tc.spec, Overlays: tc.overlays}
			assertFusedBufferedParity(t, schema, req, recs)

			// Non-vacuity: a parity assertion over two empty overlay
			// slices would pass while proving nothing. Assert the fused
			// run actually produced a layer per spec with at least one
			// Present value somewhere in its payload.
			resp, err := runFusedCrosstabViaRunner(t, schema, req, recs, false)
			if err != nil {
				t.Fatalf("fused RunCrosstabFused: %v", err)
			}
			if got, want := len(resp.Overlays), len(tc.overlays); got != want {
				t.Fatalf("fused Overlays = %d layers, want %d", got, want)
			}
			for i, layer := range resp.Overlays {
				if n := presentOverlayValues(layer); n == 0 {
					t.Errorf("overlay layer %d (%s) carries no Present value — "+
						"the parity assertion above would be vacuous", i, layer.Kind)
				}
			}
		})
	}
}

// presentOverlayValues counts populated entries across an overlay
// layer's scalar / series / matrix payload slots. Deliberately
// shape-agnostic: the point is "this layer decorated something", not
// which slot it used.
func presentOverlayValues(layer types.OverlayLayer) int {
	n := 0
	if layer.Payload.Scalar != nil {
		n++
	}
	if s := layer.Payload.Series; s != nil {
		n += len(s.Entries)
	}
	if m := layer.Payload.Matrix; m != nil {
		for _, row := range m.Cells {
			for _, cell := range row {
				if cell.Present {
					n++
				}
			}
		}
	}
	return n
}

// TestFusedCrosstab_FanOutComponentsDisabledParity pins the
// DisableComponents wire shape under fan-out. With components off the
// response must be byte-identical to the pre-Components baseline on
// BOTH paths — Components nil, not an empty struct — while the matrix
// itself is unchanged by the knob.
func TestFusedCrosstab_FanOutComponentsDisabledParity(t *testing.T) {
	cases := []struct {
		name string
		spec *types.CrosstabSpec
	}{
		{
			name: "both axes fanning",
			spec: &types.CrosstabSpec{
				Rows:    []*types.Group{setGroup("tags")},
				Columns: []*types.Group{setGroup("chans")},
				Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "sum"},
				Shape:   types.CrosstabShapeMatrix,
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
		},
		{
			name: "composed fanning axis with Include",
			spec: &types.CrosstabSpec{
				Rows:    []*types.Group{catGroup("region"), includeSetGroup("tags", "DISC", "VISA")},
				Columns: []*types.Group{catGroup("region")},
				Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "region", Label: "n"},
				Shape:   types.CrosstabShapeMatrix,
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schema := fanoutCrosstabSchema(t)
			recs := fanoutRecords(schema)
			req := &types.Request{Crosstab: tc.spec}
			assertFusableCrosstab(t, schema, req)

			bufResp, err := runBufferedCrosstabWithComponents(t, schema, req, recs, true)
			if err != nil {
				t.Fatalf("buffered RunCrosstab: %v", err)
			}
			fusedResp, err := runFusedCrosstabViaRunner(t, schema, req, recs, true)
			if err != nil {
				t.Fatalf("fused RunCrosstabFused: %v", err)
			}
			if bufResp.Components != nil {
				t.Errorf("buffered Components = %s, want nil under DisableComponents", jsonOf(t, bufResp.Components))
			}
			if fusedResp.Components != nil {
				t.Errorf("fused Components = %s, want nil under DisableComponents", jsonOf(t, fusedResp.Components))
			}
			if want, got := jsonOf(t, bufResp.Crosstab), jsonOf(t, fusedResp.Crosstab); want != got {
				t.Errorf("Crosstab diverges under DisableComponents:\nbuffered: %s\nfused:    %s", want, got)
			}

			// The knob must not move the matrix: the components-enabled
			// fused matrix has to equal the components-disabled one.
			enabled, err := runFusedCrosstabViaRunner(t, schema, req, recs, false)
			if err != nil {
				t.Fatalf("fused RunCrosstabFused (components on): %v", err)
			}
			if want, got := jsonOf(t, enabled.Crosstab), jsonOf(t, fusedResp.Crosstab); want != got {
				t.Errorf("DisableComponents changed the matrix:\non:  %s\noff: %s", want, got)
			}
		})
	}
}

// TestFusedCrosstab_FanOutIncludeOrderingIsHonoured is the non-oracle
// companion to the Include parity rows above. Include is an ORDERED
// inclusion list: reversing it must reverse the emitted axis keys on
// both paths. A parity assertion alone would pass if both paths sorted
// the keys and ignored the list entirely.
func TestFusedCrosstab_FanOutIncludeOrderingIsHonoured(t *testing.T) {
	cases := []struct {
		name    string
		axis    string // "rows" or "columns"
		include []string
	}{
		{name: "row axis dictionary order", axis: "rows", include: []string{"VISA", "DISC"}},
		{name: "row axis reversed", axis: "rows", include: []string{"DISC", "VISA"}},
		{name: "column axis dictionary order", axis: "columns", include: []string{"WEB", "ATM"}},
		{name: "column axis reversed", axis: "columns", include: []string{"ATM", "WEB"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schema := fanoutCrosstabSchema(t)
			recs := fanoutRecords(schema)
			spec := &types.CrosstabSpec{
				Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "sum"},
				Shape:   types.CrosstabShapeMatrix,
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			}
			if tc.axis == "rows" {
				spec.Rows = []*types.Group{includeSetGroup("tags", tc.include...)}
				spec.Columns = []*types.Group{catGroup("region")}
			} else {
				spec.Rows = []*types.Group{catGroup("region")}
				spec.Columns = []*types.Group{includeSetGroup("chans", tc.include...)}
			}
			req := &types.Request{Crosstab: spec}
			assertFusedBufferedParity(t, schema, req, recs)

			fused, err := runFusedCrosstabViaRunner(t, schema, req, recs, false)
			if err != nil {
				t.Fatalf("fused RunCrosstabFused: %v", err)
			}
			m := fused.Crosstab.Matrix
			if m == nil {
				t.Fatal("fused response missing Matrix payload")
			}
			keys := m.RowKeys
			if tc.axis == "columns" {
				keys = m.ColumnKeys
			}
			got := make([]string, 0, len(keys))
			for _, k := range keys {
				if len(k) == 0 {
					t.Fatalf("empty axis key tuple in %v", keys)
				}
				s, ok := k[0].(string)
				if !ok {
					t.Fatalf("axis key %v is not a string tuple", k)
				}
				got = append(got, s)
			}
			if !reflect.DeepEqual(got, tc.include) {
				t.Errorf("%s axis keys = %v, want %v (include order is author order)",
					tc.axis, got, tc.include)
			}
		})
	}
}

// TestFusedCrosstab_FanOutIncludeCanEmptyARecord covers the acceptance
// case "a set that is non-empty but empty AFTER Include filtering". The
// record still exists, still passes the filters, and still feeds the
// grand margin — it simply resolves to no bucket on the filtered axis,
// exactly as a popcount-0 mask would.
func TestFusedCrosstab_FanOutIncludeCanEmptyARecord(t *testing.T) {
	schema := fanoutCrosstabSchema(t)
	recs := fanoutRecords(schema)
	// Only AMEX survives. Of the eight fixture rows, two carry AMEX
	// (VISA|MC|AMEX and AMEX|DISC); the rest are either null, empty,
	// or non-empty-but-wholly-excluded.
	req := &types.Request{Crosstab: &types.CrosstabSpec{
		Rows:    []*types.Group{includeSetGroup("tags", "AMEX")},
		Columns: []*types.Group{setGroup("chans")},
		Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "region", Label: "n"},
		Shape:   types.CrosstabShapeMatrix,
		Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
	}}
	assertFusedBufferedParity(t, schema, req, recs)

	state, err := NewFusedCrosstabState(req.Crosstab, schema, &ExtensionRegistry{})
	if err != nil {
		t.Fatalf("NewFusedCrosstabState: %v", err)
	}
	for _, r := range recs {
		state.AddTotalRow()
		if err := state.Update(r); err != nil {
			t.Fatalf("Update: %v", err)
		}
	}
	if got, want := len(state.rowKeys), 1; got != want {
		t.Errorf("interned row keys = %d (%v), want %d — only the included label may key", got, state.rowKeys, want)
	}
	// The two AMEX rows fan over their channel masks: {WEB, POS} and
	// {WEB, POS, ATM} -> 5 cell updates.
	if got, want := state.includedRecords, 5; got != want {
		t.Errorf("includedRecords = %d, want %d", got, want)
	}
	// Row margin counts each AMEX record once per included label (one
	// label here), so 2 — NOT the 8 records the grand margin sees.
	if got, want := state.rowMarginCount[0], 2; got != want {
		t.Errorf("rowMarginCount[0] = %d, want %d", got, want)
	}
	if got, want := state.grandMarginCount, len(recs); got != want {
		t.Errorf("grandMarginCount = %d, want %d — an Include-excluded record still exists", got, want)
	}
}

// TestFusedCrosstab_FanOutAxisComponentsBucketCounts is the explicit
// MetaGrouper.Components() assertion the story calls out separately from
// cell-value equality. An implementation that derives the axis keys
// inside the cell product walk passes every cell assertion and fails
// here, because the per-label observation counts would be multiplied by
// the OTHER axis's fan width.
//
// Buffered runs the axis grouper over the full filtered set exactly
// once (Processor.axisComponentsFor), so each label's count is the
// number of RECORDS carrying it — independent of the column axis.
func TestFusedCrosstab_FanOutAxisComponentsBucketCounts(t *testing.T) {
	schema := fanoutCrosstabSchema(t)
	recs := fanoutRecords(schema)
	req := &types.Request{Crosstab: &types.CrosstabSpec{
		// The column axis fans 3 ways so a product-walk derivation
		// would inflate the row-axis counts visibly.
		Rows:    []*types.Group{setGroup("tags")},
		Columns: []*types.Group{setGroup("chans")},
		Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "region", Label: "n"},
		Shape:   types.CrosstabShapeMatrix,
		Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
	}}
	assertFusableCrosstab(t, schema, req)

	bufResp, err := runBufferedCrosstabWithComponents(t, schema, req, recs, false)
	if err != nil {
		t.Fatalf("buffered RunCrosstab: %v", err)
	}
	fusedResp, err := runFusedCrosstabViaRunner(t, schema, req, recs, false)
	if err != nil {
		t.Fatalf("fused RunCrosstabFused: %v", err)
	}

	// Hand-computed from fanoutRecords: VISA on rows 1+2, MC on rows
	// 1+6, AMEX on rows 1+7, DISC on rows 5+6+7.
	wantRows := map[string]int{"VISA": 2, "MC": 2, "AMEX": 2, "DISC": 3}
	// WEB on rows 1, 2, 6, 7; POS on rows 1, 4(null tags), 7; ATM on
	// rows 3(empty tags), 6, 7. The column position counts records
	// independently of whether the ROW axis resolved.
	wantCols := map[string]int{"WEB": 4, "POS": 3, "ATM": 3}

	for _, side := range []struct {
		label string
		resp  *types.Response
	}{{"buffered", bufResp}, {"fused", fusedResp}} {
		ct := side.resp.Components.Crosstab
		if got := singleAxisBucketCounts(t, ct.RowKeyComponents); !reflect.DeepEqual(got, wantRows) {
			t.Errorf("%s row-axis bucket counts = %v, want %v", side.label, got, wantRows)
		}
		if got := singleAxisBucketCounts(t, ct.ColumnKeyComponents); !reflect.DeepEqual(got, wantCols) {
			t.Errorf("%s column-axis bucket counts = %v, want %v", side.label, got, wantCols)
		}
	}

	// total_label_observations is the grouper's own sum over its
	// buckets; it is emitted per key, so every entry repeats it. Assert
	// it once per path against the hand-computed sums.
	assertTotalLabelObservations(t, "buffered", bufResp, 9, 10)
	assertTotalLabelObservations(t, "fused", fusedResp, 9, 10)
}

// TestFusedCrosstab_FanOutCellFloorIsHandChecked pins the per-cell
// universal floor {n, n_null} to hand-computed numbers on BOTH paths.
// The parity harness compares the two Components blobs to each other;
// this one compares them to the arithmetic, so a fan-out defect that
// mis-routes the SAME way on both paths (a shared helper counting a
// record once per label where it should count it once, say) still
// fails.
//
// Fixture (rows = tags fan, columns = region, cell = sum over "value"):
//
//	VISA -> rec0 (north,10), rec1 (north,20)
//	MC   -> rec0 (north,10), rec5 (south, NULL value)
//	AMEX -> rec0 (north,10), rec6 (north,7)
//	DISC -> rec4 (south,50), rec5 (south, NULL value), rec6 (north,7)
func TestFusedCrosstab_FanOutCellFloorIsHandChecked(t *testing.T) {
	schema := fanoutCrosstabSchema(t)
	recs := fanoutRecords(schema)
	req := &types.Request{Crosstab: &types.CrosstabSpec{
		Rows:    []*types.Group{setGroup("tags")},
		Columns: []*types.Group{catGroup("region")},
		Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "sum"},
		Shape:   types.CrosstabShapeMatrix,
		Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
	}}
	assertFusedBufferedParity(t, schema, req, recs)

	// {label, region} -> {n, n_null}. Absent coordinates are asserted
	// absent (no cell, no floor entry). NOTE the floor's `n` counts
	// NON-NULL observations — a cell holding only the null-"value"
	// record reports {0, 1}, and CellCounts (the record count) is
	// n + n_null, which the loop below asserts coordinate by
	// coordinate.
	type floor struct{ n, nNull int }
	want := map[string]floor{
		"VISA|north": {2, 0},
		"MC|north":   {1, 0},
		"MC|south":   {0, 1},
		"AMEX|north": {2, 0},
		"DISC|north": {1, 0},
		"DISC|south": {1, 1},
	}

	bufResp, err := runBufferedCrosstabWithComponents(t, schema, req, recs, false)
	if err != nil {
		t.Fatalf("buffered RunCrosstab: %v", err)
	}
	fusedResp, err := runFusedCrosstabViaRunner(t, schema, req, recs, false)
	if err != nil {
		t.Fatalf("fused RunCrosstabFused: %v", err)
	}

	for _, side := range []struct {
		label string
		resp  *types.Response
	}{{"buffered", bufResp}, {"fused", fusedResp}} {
		m := side.resp.Crosstab.Matrix
		ct := side.resp.Components.Crosstab
		got := map[string]floor{}
		for r, rowKey := range m.RowKeys {
			for c, colKey := range m.ColumnKeys {
				if !m.Cells[r][c].Present {
					continue
				}
				key := axisKeyString(t, rowKey) + "|" + axisKeyString(t, colKey)
				cell := ct.CellComponents[r][c]
				got[key] = floor{
					n:     int(coerceFloat64(cell["n"])),
					nNull: int(coerceFloat64(cell["n_null"])),
				}
				// CellCounts is the RECORD count for the cell; the
				// floor splits it into non-null + null. Under fan-out
				// both must count the record once per bucket it fans
				// into, so the identity has to hold at every
				// coordinate.
				if n, want := ct.CellCounts[r][c], got[key].n+got[key].nNull; n != want {
					t.Errorf("%s: CellCounts[%d][%d] = %d, want n+n_null = %d",
						side.label, r, c, n, want)
				}
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: per-cell {n, n_null} = %v, want %v", side.label, got, want)
		}
	}
}

// axisKeyString renders a single-position axis key tuple as its label.
func axisKeyString(t *testing.T, key types.AxisKey) string {
	t.Helper()
	if len(key) != 1 {
		t.Fatalf("axis key %v is not single-position", key)
	}
	s, ok := key[0].(string)
	if !ok {
		t.Fatalf("axis key %v is not a string tuple", key)
	}
	return s
}

// singleAxisBucketCounts folds a single-position axis's per-key
// components vector into {label: count}. Single-position axes emit the
// bucket map directly (projectAxisKeyComponents), so no "axes" wrapper
// is expected here.
func singleAxisBucketCounts(t *testing.T, entries []map[string]any) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, entry := range entries {
		if entry == nil {
			t.Fatalf("nil axis-key components entry in %v", entries)
		}
		key, ok := entry["key"].(string)
		if !ok {
			t.Fatalf("axis-key components entry %#v has no string key", entry)
		}
		out[key] = int(coerceFloat64(entry["count"]))
	}
	return out
}

// assertTotalLabelObservations checks the GROUP_SET_PER_ELEMENT
// total_label_observations counter on both axes. The counter sums
// buckets[].count and therefore EXCEEDS the record count under fan-out
// — that over-count is the documented contract, not a bug, and it must
// be identical on both paths.
func assertTotalLabelObservations(t *testing.T, label string, resp *types.Response, wantRow, wantCol int) {
	t.Helper()
	// Re-derive from the per-key entries: every entry for a given axis
	// carries the same axis-wide totals via its owning grouper, so the
	// assertion is on the summed bucket counts instead.
	ct := resp.Components.Crosstab
	sum := func(entries []map[string]any) int {
		total := 0
		for _, e := range entries {
			total += int(coerceFloat64(e["count"]))
		}
		return total
	}
	if got := sum(ct.RowKeyComponents); got != wantRow {
		t.Errorf("%s: summed row-axis label observations = %d, want %d", label, got, wantRow)
	}
	if got := sum(ct.ColumnKeyComponents); got != wantCol {
		t.Errorf("%s: summed column-axis label observations = %d, want %d", label, got, wantCol)
	}
}
