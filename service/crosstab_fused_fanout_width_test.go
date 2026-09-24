package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/fs"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// Set-width breadth for the fan-out equivalence harness.
//
// crosstab_fused_fanout_test.go drives one real cohort carrying a
// set_u8 field; these cases run the same crosstab at every registered
// set rung through a real .pulse file, selecting a bit ABOVE the
// previous rung's ceiling so a truncating decode (a mask read at the
// wrong width) drops a label the buffered path keeps.
//
// This used to be justified by "width matters only at the decode
// boundary, because processing.Record.SetValue hands the grouper a
// uint64 whatever the on-wire width was". Both halves of that are now
// false. SetValue is gone — processing.Record.SetMaskValue is the one
// set accessor and it returns an encoding.SetMask — and the wide rungs
// (set_u128, set_u256) carry a genuinely different in-memory shape on
// the Record than the narrow ones, whose uint64 storage is unchanged.
// So a processing-level width table is NOT vacuous any more and it does
// exist: processing/grouper_set_wide_test.go
// (TestGroupSetWide_NarrowRungsAreUnchanged) runs the group-key
// derivation across all six rungs with no file in sight. The two tables
// answer different questions and both are needed — that one asks
// whether the KEY covers every word, this one asks whether the bytes
// survive the round trip.

// setWidthLabels builds n synthetic dictionary labels, L000..L(n-1).
// Zero-padded to three digits so the dictionary stays readable past 64
// members and so lexicographic order tracks bit order.
func setWidthLabels(n int) []string {
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, setWidthLabel(i))
	}
	return out
}

// setWidthLabel is the dictionary label for bit i.
func setWidthLabel(i int) string { return fmt.Sprintf("L%03d", i) }

// setWidthSchema is the setFanoutSchema shape with the set field's
// width and dictionary size parameterised: region (categorical_u8),
// tags (set_uN over labelCount labels), value (f64).
//
// labelCount is deliberately allowed past 64: a set_u128 / set_u256
// dictionary is exactly the case the narrow rungs cannot express.
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

// writeWideSetPulseFile writes a complete .pulse file whose records are
// described as []any per field: a uint64 for every type the uint64 value
// API accepts, and an encoding.SetMask for the wide set rungs, which
// encoding.WriteFieldValue refuses outright rather than persisting a
// truncated low word.
func writeWideSetPulseFile(t *testing.T, schema *encoding.Schema, records [][]any) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := encoding.WriteHeader(&buf); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := encoding.WriteSchema(&buf, schema); err != nil {
		t.Fatalf("WriteSchema: %v", err)
	}
	for ri, rec := range records {
		for fi, field := range schema.Fields {
			switch v := rec[fi].(type) {
			case encoding.SetMask:
				if err := encoding.WriteSetMask(&buf, field.Type, v); err != nil {
					t.Fatalf("WriteSetMask record[%d] field[%d]: %v", ri, fi, err)
				}
			case uint64:
				if err := encoding.WriteFieldValue(&buf, field.Type, v); err != nil {
					t.Fatalf("WriteFieldValue record[%d] field[%d]: %v", ri, fi, err)
				}
			default:
				t.Fatalf("record[%d] field[%d]: unsupported value type %T", ri, fi, rec[fi])
			}
		}
	}
	return buf.Bytes()
}

// setupWideSetTestFS is setupTestFS with the wide-capable record writer.
func setupWideSetTestFS(t *testing.T, path string, schema *encoding.Schema, records [][]any) *fs.Config {
	t.Helper()
	cfg := fs.NewMemMap()
	if err := afero.WriteFile(cfg.Fs(), path, writeWideSetPulseFile(t, schema, records), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return cfg
}

// setWidthMaskValue renders a mask for the rung under test in the shape
// writeWideSetPulseFile wants: a uint64 for the narrow rungs (their
// storage is unchanged) and an encoding.SetMask for the wide ones.
func setWidthMaskValue(ft encoding.FieldType, bits ...int) any {
	var m encoding.SetMask
	for _, b := range bits {
		m = m.WithBit(b)
	}
	if ft.IsWideSet() {
		return m
	}
	low, _ := m.Uint64()
	return low
}

// TestCrosstabFused_SetWidthFanOutMatchesBuffered runs the fan-out
// crosstab over a real cohort at every set width. Each width selects a
// bit that only exists at that width (bit 11 for u16, bit 19 for u32,
// bit 39 for u64, bit 100 for u128, bit 200 for u256), so a decode that
// truncates the mask to a narrower integer loses that label on the
// fused path only.
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
		{name: "set_u128", typ: encoding.FieldTypeSetU128, labels: 110, highBit: 100},
		{name: "set_u256", typ: encoding.FieldTypeSetU256, labels: 206, highBit: 200},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			schema := setWidthSchema(t, tc.typ, tc.labels)
			mask := func(bits ...int) any { return setWidthMaskValue(tc.typ, bits...) }
			records := [][]any{
				// north: bits 0+1 plus the width's top bit -> 3 labels.
				{uint64(0), mask(0, 1, tc.highBit), uint64(math.Float64bits(10))},
				// north: bit 0 alone.
				{uint64(0), mask(0), uint64(math.Float64bits(20))},
				// south: bit 1 plus the top bit.
				{uint64(1), mask(1, tc.highBit), uint64(math.Float64bits(30))},
				// south: empty mask — a valid "no selection".
				{uint64(1), mask(), uint64(math.Float64bits(40))},
				// south: the top bit alone.
				{uint64(1), mask(tc.highBit), uint64(math.Float64bits(50))},
			}
			cfg := setupWideSetTestFS(t, "widths.pulse", schema, records)
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
			highLabel := setWidthLabel(tc.highBit)
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
