package service

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/processing"
	"github.com/frankbardon/pulse/types"
)

// setFanoutSchema is a real on-disk cohort shape carrying a set_u8
// "tags" field alongside a categorical "region" and an f64 "value", so
// the GROUP_SET_PER_ELEMENT crosstab can be driven through the full
// service dispatch rather than the processing-level state directly.
func setFanoutSchema(t *testing.T) *encoding.Schema {
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
			{Name: "region", Type: encoding.FieldTypeCategoricalU8, ByteOffset: 0, CsvColumnIdx: 0, Dictionary: regionDict},
			{Name: "tags", Type: encoding.FieldTypeSetU8, ByteOffset: 1, CsvColumnIdx: 1, Dictionary: tagsDict},
			{Name: "value", Type: encoding.FieldTypeF64, ByteOffset: 2, CsvColumnIdx: 2},
		},
	}
}

// setFanoutRecords: masks chosen so several records select multiple
// labels (the fan fires), one selects a single label, and one selects
// nothing (empty mask is a valid "no selection", distinct from null).
func setFanoutRecords() [][]uint64 {
	return [][]uint64{
		{0, 0b0111, math.Float64bits(10)}, // north, VISA+MC+AMEX
		{0, 0b0001, math.Float64bits(20)}, // north, VISA
		{1, 0b1100, math.Float64bits(30)}, // south, AMEX+DISC
		{1, 0b0000, math.Float64bits(40)}, // south, no selection
		{1, 0b1010, math.Float64bits(50)}, // south, MC+DISC
	}
}

// TestCrosstabFused_SetPerElementRoutesThroughFusedPath is the
// service-level expression of E2-S3: a GROUP_SET_PER_ELEMENT crosstab
// must (a) be accepted by the fusion gate, (b) actually run down the
// fused arm, and (c) return the same wire payload as the buffered arm —
// rather than erroring or silently dropping the buckets a record fans
// into, which is what the E2-S2 first-key shim did.
func TestCrosstabFused_SetPerElementRoutesThroughFusedPath(t *testing.T) {
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
		}
	}

	svcFused := New(cfg)
	if ok, reason := processing.CanFuseCrosstab(buildReq(), schema, svcFused.Extensions()); !ok {
		t.Fatalf("CanFuseCrosstab rejected a GROUP_SET_PER_ELEMENT crosstab: %s", reason)
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

	marshal := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("json.Marshal: %v", err)
		}
		return string(b)
	}
	if want, got := marshal(bufResp.Crosstab), marshal(fusedResp.Crosstab); want != got {
		t.Errorf("Crosstab diverges:\nbuffered: %s\nfused:    %s", want, got)
	}
	if want, got := marshal(bufResp.Components), marshal(fusedResp.Components); want != got {
		t.Errorf("Components diverge:\nbuffered: %s\nfused:    %s", want, got)
	}

	// Explicit fan assertion so a future regression that quietly drops
	// buckets cannot pass by matching an equally-broken buffered path:
	// four labels are selected across the cohort, so four rows.
	m := fusedResp.Crosstab.Matrix
	if m == nil {
		t.Fatal("fused response missing Matrix payload")
	}
	if got, want := len(m.RowKeys), 4; got != want {
		t.Fatalf("RowKeys = %d (%v), want %d (one per selected label)", got, m.RowKeys, want)
	}
	// The 3-label north record (value 10) must reach VISA, MC and AMEX.
	byLabel := map[string]int{}
	for i, rk := range m.RowKeys {
		byLabel[rk[0].(string)] = i
	}
	north := 0
	for _, label := range []string{"VISA", "MC", "AMEX"} {
		idx, ok := byLabel[label]
		if !ok {
			t.Fatalf("row key %q missing from %v", label, m.RowKeys)
		}
		cell := m.Cells[idx][north]
		if !cell.Present {
			t.Fatalf("(%s, north) cell absent — the fan dropped a bucket", label)
		}
	}
	// VISA/north carries records 0 and 1 -> 30; MC/north carries only
	// record 0 -> 10. A first-key shim would have left MC/north empty.
	if got := m.Cells[byLabel["VISA"]][north].Value; got != 30.0 {
		t.Errorf("(VISA, north) = %v, want 30", got)
	}
	if got := m.Cells[byLabel["MC"]][north].Value; got != 10.0 {
		t.Errorf("(MC, north) = %v, want 10", got)
	}
}
