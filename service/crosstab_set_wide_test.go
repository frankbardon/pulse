package service

import (
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/frankbardon/pulse/descriptor"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// E2-S3 — wide-set groupers through the real service stack.
//
// The processing-level tests drive Records built in memory. These drive
// a real .pulse file, so the decode boundary is part of the assertion:
// a mask read at the wrong width loses a label rather than merely
// disagreeing with a second in-process arm.

// wideSetGrouperSchema carries a categorical region, a set_uN "tags"
// column over labelCount members, and an f64 value.
func wideSetGrouperSchema(t *testing.T, ft encoding.FieldType, labelCount int) *encoding.Schema {
	t.Helper()
	regionDict := encoding.NewDictionary()
	for _, r := range []string{"north", "south"} {
		if _, err := regionDict.Add(r); err != nil {
			t.Fatalf("region dict.Add: %v", err)
		}
	}
	tagsDict := encoding.NewDictionary()
	for i := 0; i < labelCount; i++ {
		if _, err := tagsDict.Add(setWidthLabel(i)); err != nil {
			t.Fatalf("tags dict.Add: %v", err)
		}
	}
	return &encoding.Schema{
		Fields: []encoding.Field{
			{Name: "region", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: regionDict},
			{Name: "tags", Type: ft, ByteOffset: 1, CsvColumnIdx: 1, Dictionary: tagsDict},
			{Name: "value", Type: encoding.FieldTypeF64, ByteOffset: 1 + ft.ByteSize(), CsvColumnIdx: 2},
		},
	}
}

// wideSetGrouperCohort writes a 206-member set_u256 cohort whose masks
// span all four words. Returns the config, the schema and the per-record
// (region, bits, value) truth the assertions are derived from.
type wideSetRow struct {
	region int
	bits   []int
	value  float64
}

func wideSetGrouperCohort(t *testing.T, path string) (*encoding.Schema, []wideSetRow, afero.Fs, *Service) {
	t.Helper()
	schema := wideSetGrouperSchema(t, encoding.FieldTypeSetU256, 206)
	rows := []wideSetRow{
		{region: 0, bits: []int{1, 70, 200}, value: 10},
		{region: 0, bits: []int{1}, value: 20},
		{region: 1, bits: []int{70, 200}, value: 30},
		{region: 1, bits: nil, value: 40}, // empty mask: a valid "no selection"
		{region: 1, bits: []int{201}, value: 50},
		{region: 0, bits: []int{130}, value: 60},
		// These two differ ONLY above bit 192 — the pair that collides
		// if the group key stops at any word boundary.
		{region: 0, bits: []int{1, 200}, value: 70},
		{region: 0, bits: []int{1, 201}, value: 80},
	}
	records := make([][]any, 0, len(rows))
	for _, r := range rows {
		records = append(records, []any{
			uint64(r.region),
			setWidthMaskValue(encoding.FieldTypeSetU256, r.bits...),
			uint64(math.Float64bits(r.value)),
		})
	}
	cfg := setupWideSetTestFS(t, path, schema, records)
	return schema, rows, cfg.Fs(), New(cfg)
}

// TestWideSetGrouper_PerElementAxisThroughService fans a 206-member
// set_u256 column onto the crosstab ROW axis through Service.Process,
// on both the fused and the buffered arm, and asserts each against
// per-bit totals derived from the source rows.
func TestWideSetGrouper_PerElementAxisThroughService(t *testing.T) {
	schema, rows, _, _ := wideSetGrouperCohort(t, "wide_sets.pulse")
	_, _, _, svcFused := wideSetGrouperCohort(t, "wide_sets.pulse")
	ctx := context.Background()

	buildReq := func() *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: "wide_sets.pulse"},
			Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_SET_PER_ELEMENT, Field: "tags"}},
				Columns: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "total"},
				Shape:   types.CrosstabShapeMatrix,
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
		}
	}
	if ok, reason := processing.CanFuseCrosstab(buildReq(), schema, svcFused.Extensions()); !ok {
		t.Fatalf("CanFuseCrosstab rejected a wide-set fan-out crosstab: %s", reason)
	}
	fusedResp, err := svcFused.Process(ctx, buildReq())
	if err != nil {
		t.Fatalf("Process (fused): %v", err)
	}

	_, _, _, svcBuf := wideSetGrouperCohort(t, "wide_sets.pulse")
	svcBuf.SetDisableCrosstabFusion(true)
	bufResp, err := svcBuf.Process(ctx, buildReq())
	if err != nil {
		t.Fatalf("Process (buffered): %v", err)
	}
	assertResponseSlotsEqual(t, bufResp, fusedResp)

	// Generated truth: (bit, region) -> summed value, straight off rows.
	regionName := []string{"north", "south"}
	want := map[string]map[string]float64{}
	for _, r := range rows {
		for _, b := range r.bits {
			label := setWidthLabel(b)
			if want[label] == nil {
				want[label] = map[string]float64{}
			}
			want[label][regionName[r.region]] += r.value
		}
	}

	for _, arm := range []struct {
		name string
		resp *types.Response
	}{{"fused", fusedResp}, {"buffered", bufResp}} {
		t.Run(arm.name, func(t *testing.T) {
			m := arm.resp.Crosstab.Matrix
			if m == nil {
				t.Fatal("no matrix payload")
			}
			if got := len(m.RowKeys); got != len(want) {
				t.Fatalf("row keys = %d (%v), want %d — a truncated mask collapses high bits", got, m.RowKeys, len(want))
			}
			colIdx := map[string]int{}
			for i, ck := range m.ColumnKeys {
				s, _ := ck[0].(string)
				colIdx[s] = i
			}
			for i, rk := range m.RowKeys {
				label, _ := rk[0].(string)
				cells, known := want[label]
				if !known {
					t.Fatalf("unexpected row key %q", label)
				}
				for region, w := range cells {
					cell := m.Cells[i][colIdx[region]]
					if !cell.Present {
						t.Fatalf("(%s,%s) not present", label, region)
					}
					if got, _ := cell.Value.(float64); got != w {
						t.Errorf("(%s,%s) = %v, want %v", label, region, cell.Value, w)
					}
				}
			}
			// Margins recompute from raw rows: the grand total is the
			// plain sum over every record, NOT the sum over the fanned
			// cells (which over-counts multi-label rows).
			var wantGrand float64
			for _, r := range rows {
				wantGrand += r.value
			}
			if got, _ := m.GrandTotal.Value.(float64); got != wantGrand {
				t.Errorf("grand margin = %v, want %v (raw rows, not cells)", m.GrandTotal.Value, wantGrand)
			}
		})
	}
}

// TestWideSetGrouper_SetValueOnColumnAxisThroughService puts the atomic
// mask grouper on the COLUMN axis. Two rows whose masks differ only at
// bits 200 and 201 must occupy two different columns.
func TestWideSetGrouper_SetValueOnColumnAxisThroughService(t *testing.T) {
	schema, rows, _, svcFused := wideSetGrouperCohort(t, "wide_sets.pulse")
	ctx := context.Background()

	buildReq := func() *types.Request {
		return &types.Request{
			Cohort: &types.Cohort{Filename: "wide_sets.pulse"},
			Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "region"}},
				Columns: []*types.Group{{Type: types.GROUP_SET_VALUE, Field: "tags"}},
				Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "value", Label: "total"},
				Shape:   types.CrosstabShapeMatrix,
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
			},
		}
	}
	if ok, reason := processing.CanFuseCrosstab(buildReq(), schema, svcFused.Extensions()); !ok {
		t.Fatalf("CanFuseCrosstab rejected a wide-set atomic-mask axis: %s", reason)
	}
	fusedResp, err := svcFused.Process(ctx, buildReq())
	if err != nil {
		t.Fatalf("Process (fused): %v", err)
	}
	_, _, _, svcBuf := wideSetGrouperCohort(t, "wide_sets.pulse")
	svcBuf.SetDisableCrosstabFusion(true)
	bufResp, err := svcBuf.Process(ctx, buildReq())
	if err != nil {
		t.Fatalf("Process (buffered): %v", err)
	}
	assertResponseSlotsEqual(t, bufResp, fusedResp)

	// Generated truth: one column per DISTINCT mask, keyed by the sorted
	// pipe-joined label list.
	want := map[string]float64{}
	for _, r := range rows {
		key := ""
		for i, b := range r.bits {
			if i > 0 {
				key += "|"
			}
			key += setWidthLabel(b)
		}
		want[key] += r.value
	}
	m := fusedResp.Crosstab.Matrix
	if got := len(m.ColumnKeys); got != len(want) {
		t.Fatalf("column keys = %d (%v), want %d distinct masks", got, m.ColumnKeys, len(want))
	}
	for _, key := range []string{
		setWidthLabel(1) + "|" + setWidthLabel(200),
		setWidthLabel(1) + "|" + setWidthLabel(201),
	} {
		found := false
		for _, ck := range m.ColumnKeys {
			if s, _ := ck[0].(string); s == key {
				found = true
			}
		}
		if !found {
			t.Errorf("column key %q missing from %v — two masks differing only above bit 192 collided", key, m.ColumnKeys)
		}
	}
	for ci, ck := range m.ColumnKeys {
		key, _ := ck[0].(string)
		w, known := want[key]
		if !known {
			t.Fatalf("unexpected column key %q (want %v)", key, want)
		}
		var got float64
		for ri := range m.RowKeys {
			if m.Cells[ri][ci].Present {
				v, _ := m.Cells[ri][ci].Value.(float64)
				got += v
			}
		}
		if got != w {
			t.Errorf("column %q summed %v across rows, want %v", key, got, w)
		}
	}
}

// TestWideSetGrouper_PredictStreamableAgreesWithRuntime is the
// streamability parity assertion the story calls for: there is no
// decimal128-style width gate on the set rungs, so PredictResult.
// Streamable and processing.CanStreamRequest must BOTH say yes for a
// wide-set grouped request — and the run must actually take the
// streaming path.
func TestWideSetGrouper_PredictStreamableAgreesWithRuntime(t *testing.T) {
	for _, ft := range []encoding.FieldType{
		encoding.FieldTypeSetU8, encoding.FieldTypeSetU64,
		encoding.FieldTypeSetU128, encoding.FieldTypeSetU256,
	} {
		for _, gt := range []types.GroupType{types.GROUP_SET_VALUE, types.GROUP_SET_PER_ELEMENT} {
			t.Run(fmt.Sprintf("%s/%s", ft, gt), func(t *testing.T) {
				labels := 8
				if ft.IsWideSet() {
					labels = 100
				}
				schema := wideSetGrouperSchema(t, ft, labels)
				records := [][]any{
					{uint64(0), setWidthMaskValue(ft, 0, 3), uint64(math.Float64bits(10))},
					{uint64(1), setWidthMaskValue(ft, 3), uint64(math.Float64bits(20))},
				}
				cfg := setupWideSetTestFS(t, "stream.pulse", schema, records)
				data, err := afero.ReadFile(cfg.Fs(), "stream.pulse")
				if err != nil {
					t.Fatalf("ReadFile: %v", err)
				}

				req := &types.Request{
					Cohort:       &types.Cohort{Filename: "stream.pulse"},
					Groups:       []*types.Group{{Type: gt, Field: "tags"}},
					Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "value", Label: "total"}},
				}
				runtime := processing.CanStreamRequest(req, schema)
				env := descriptor.PredictFromBytes(data, req, nil)
				pred, ok := env.Data.(*descriptor.PredictResult)
				if !ok {
					t.Fatalf("Predict data = %T, want *descriptor.PredictResult", env.Data)
				}
				if pred.Streamable != runtime {
					t.Fatalf("PredictResult.Streamable = %v but processing.CanStreamRequest = %v (reasons: %v)",
						pred.Streamable, runtime, pred.StreamableReasons)
				}
				if !runtime {
					t.Fatalf("a wide-set grouped request must stream; no width gate was intended (reasons: %v)", pred.StreamableReasons)
				}
			})
		}
	}
}
