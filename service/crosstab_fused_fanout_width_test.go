package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
)

// E2-S4 — set-width breadth for the fan-out equivalence harness.
//
// The E2-S3 service test (crosstab_fused_fanout_test.go) drives one
// real cohort carrying a set_u8 field. Width matters at the DECODE
// boundary, not inside processing: processing.Record.SetValue hands the
// grouper a uint64 mask whatever the on-wire width was, so a
// processing-level table over the four widths would be vacuous. These
// cases therefore go through a real .pulse file per width, with a
// selected bit ABOVE the previous width's ceiling so a truncating
// decode (mask read as the wrong integer size) drops a label the
// buffered path keeps.

// setWidthLabels builds n synthetic dictionary labels, L00..L(n-1).
func setWidthLabels(n int) []string {
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, fmt.Sprintf("L%02d", i))
	}
	return out
}

// setWidthSchema is the setFanoutSchema shape with the set field's
// width and dictionary size parameterised: region (categorical_u8),
// tags (set_uN over labelCount labels), value (f64).
func setWidthSchema(t *testing.T, setType encoding.FieldType, labelCount int) *encoding.Schema {
	t.Helper()
	regionDict := encoding.NewDictionary()
	for _, r := range []string{"north", "south"} {
		if _, err := regionDict.Add(r); err != nil {
			t.Fatalf("region dict.Add: %v", err)
		}
	}
	tagsDict := encoding.NewDictionary()
	for _, v := range setWidthLabels(labelCount) {
		if _, err := tagsDict.Add(v); err != nil {
			t.Fatalf("tags dict.Add: %v", err)
		}
	}
	setBytes := setType.ByteSize()
	if setBytes == 0 {
		t.Fatalf("set type %v reports a zero byte size", setType)
	}
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "region", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: regionDict},
			{Name: "tags", Type: setType, ByteOffset: 1, CsvColumnIdx: 1, Dictionary: tagsDict},
			{Name: "value", Type: encoding.FieldTypeF64, ByteOffset: 1 + setBytes, CsvColumnIdx: 2},
		},
	}
}

// TestCrosstabFused_SetWidthFanOutMatchesBuffered runs the fan-out
// crosstab over a real cohort at every set width. Each width selects a
// bit that only exists at that width (bit 11 for u16, bit 19 for u32,
// bit 39 for u64), so a decode that truncates the mask to a narrower
// integer loses that label on the fused path only.
func TestCrosstabFused_SetWidthFanOutMatchesBuffered(t *testing.T) {
	cases := []struct {
		name string
		typ  encoding.FieldType
		// labels is the dictionary size; highBit is the bit index that
		// exercises the top of this width.
		labels  int
		highBit int
	}{
		{name: "set_u8", typ: encoding.FieldTypeSetU8, labels: 8, highBit: 7},
		{name: "set_u16", typ: encoding.FieldTypeSetU16, labels: 12, highBit: 11},
		{name: "set_u32", typ: encoding.FieldTypeSetU32, labels: 20, highBit: 19},
		{name: "set_u64", typ: encoding.FieldTypeSetU64, labels: 40, highBit: 39},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schema := setWidthSchema(t, tc.typ, tc.labels)
			high := uint64(1) << tc.highBit
			records := [][]uint64{
				// north: bits 0+1 plus the width's top bit -> 3 labels.
				{0, 0b11 | high, math.Float64bits(10)},
				// north: bit 0 alone.
				{0, 0b1, math.Float64bits(20)},
				// south: bit 1 plus the top bit.
				{1, 0b10 | high, math.Float64bits(30)},
				// south: empty mask — a valid "no selection".
				{1, 0, math.Float64bits(40)},
				// south: the top bit alone.
				{1, high, math.Float64bits(50)},
			}
			cfg := setupTestFS(t, "widths.pulse", schema, records)
			ctx := context.Background()

			buildReq := func() *types.Request {
				return &types.Request{
					Cohort: &types.Cohort{Filename: "widths.pulse"},
					Crosstab: &types.CrosstabSpec{
						Rows:    []*types.Group{{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}},
						Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
						Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "total"},
						Shape:   types.CrosstabShapeMatrix,
						Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
					},
				}
			}

			svcFused := New(cfg)
			if ok, reason := processing.CanFuseCrosstab(buildReq(), schema, svcFused.Extensions()); !ok {
				t.Fatalf("CanFuseCrosstab rejected a %s fan-out crosstab: %s", tc.name, reason)
			}
			fusedResp, err := svcFused.Process(ctx, buildReq())
			if err != nil {
				t.Fatalf("Process (fused): %v", err)
			}

			svcBuf := New(cfg)
			svcBuf.SetDisableCrosstabFusion(true)
			bufResp, err := svcBuf.Process(ctx, buildReq())
			if err != nil {
				t.Fatalf("Process (buffered): %v", err)
			}

			assertResponseSlotsEqual(t, bufResp, fusedResp)

			// Non-oracle: the top-of-width label must be present with
			// the right total on the fused path. A truncating decode
			// would drop the row entirely (or land it on the wrong
			// label) rather than merely disagreeing with buffered.
			m := fusedResp.Crosstab.Matrix
			if m == nil {
				t.Fatal("fused response missing Matrix payload")
			}
			highLabel := fmt.Sprintf("L%02d", tc.highBit)
			idx := -1
			for i, rk := range m.RowKeys {
				if s, _ := rk[0].(string); s == highLabel {
					idx = i
				}
			}
			if idx < 0 {
				t.Fatalf("row key %q missing from %v — the mask was truncated below bit %d",
					highLabel, m.RowKeys, tc.highBit)
			}
			// north (col 0) carries record 0 -> 10; south (col 1)
			// carries records 2 and 4 -> 80.
			if got := m.Cells[idx][0].Value; got != 10.0 {
				t.Errorf("(%s, north) = %v, want 10", highLabel, got)
			}
			if got := m.Cells[idx][1].Value; got != 80.0 {
				t.Errorf("(%s, south) = %v, want 80", highLabel, got)
			}
			// Three of the five records select the top bit or bit 0/1
			// each; the fan is real, so the row count is 3 (bit0, bit1,
			// highBit) — the empty-mask record keys nothing.
			if got, want := len(m.RowKeys), 3; got != want {
				t.Errorf("RowKeys = %d (%v), want %d", got, m.RowKeys, want)
			}
		})
	}
}

// assertResponseSlotsEqual diffs the payload-reachable slots a fused /
// buffered crosstab pair must agree on. Metadata is compared field by
// field elsewhere; here the wire form is the contract.
func assertResponseSlotsEqual(t *testing.T, want, got *types.Response) {
	t.Helper()
	marshal := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("json.Marshal: %v", err)
		}
		return string(b)
	}
	for _, slot := range []struct {
		name string
		want any
		got  any
	}{
		{"Crosstab", want.Crosstab, got.Crosstab},
		{"Data", want.Data, got.Data},
		{"Components", want.Components, got.Components},
		{"Overlays", want.Overlays, got.Overlays},
		{"Warnings", want.Warnings, got.Warnings},
	} {
		if w, g := marshal(slot.want), marshal(slot.got); w != g {
			t.Errorf("%s diverges:\nbuffered: %s\nfused:    %s", slot.name, w, g)
		}
	}
}

// TestCrosstabFused_SetFanOutWithOverlaysThroughService is the
// end-to-end expression of the combination this effort exists for: E1
// let overlays ride the fused path, E2 let fan-out axes ride it, and
// this drives both through Service.Process over a real cohort rather
// than through the processing-level orchestrator.
func TestCrosstabFused_SetFanOutWithOverlaysThroughService(t *testing.T) {
	schema := setFanoutSchema(t)
	cfg := setupTestFS(t, "sets.pulse", schema, setFanoutRecords())
	ctx := context.Background()

	buildReq := func() *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: "sets.pulse"},
			Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "total"},
				Shape:   types.CrosstabShapeMatrix,
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
			Overlays: []types.OverlaySpec{
				{
					Name:  "row_index",
					Kind:  types.OverlayKindIndexVsMargin,
					Scope: types.OverlayScopeCell,
					Ref:   types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisRow}},
				},
				{
					Name:  "col_share",
					Kind:  types.OverlayKindShareOfCol,
					Scope: types.OverlayScopeCell,
					Ref:   types.OverlayRef{Margin: &types.OverlayMarginRef{Axis: types.MarginAxisColumn}},
				},
			},
		}
	}

	svcFused := New(cfg)
	if ok, reason := processing.CanFuseCrosstab(buildReq(), schema, svcFused.Extensions()); !ok {
		t.Fatalf("CanFuseCrosstab rejected an overlay-carrying fan-out crosstab: %s", reason)
	}
	fusedResp, err := svcFused.Process(ctx, buildReq())
	if err != nil {
		t.Fatalf("Process (fused): %v", err)
	}

	svcBuf := New(cfg)
	svcBuf.SetDisableCrosstabFusion(true)
	bufResp, err := svcBuf.Process(ctx, buildReq())
	if err != nil {
		t.Fatalf("Process (buffered): %v", err)
	}

	assertResponseSlotsEqual(t, bufResp, fusedResp)

	// Non-vacuity: both layers must exist and carry a Present value,
	// otherwise the diff above compares two empty slices.
	if got, want := len(fusedResp.Overlays), 2; got != want {
		t.Fatalf("fused Overlays = %d layers, want %d", got, want)
	}
	for i, layer := range fusedResp.Overlays {
		m := layer.Payload.Matrix
		if m == nil {
			t.Fatalf("overlay layer %d (%s) has no matrix payload", i, layer.Kind)
		}
		present := 0
		for _, row := range m.Cells {
			for _, cell := range row {
				if cell.Present {
					present++
				}
			}
		}
		if present == 0 {
			t.Errorf("overlay layer %d (%s) decorated nothing", i, layer.Kind)
		}
	}
}
