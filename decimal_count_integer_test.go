package pulse_test

import (
	"context"
	"testing"

	"github.com/spf13/afero"

	"github.com/frankbardon/pulse"
	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/types"
)

// decimalCountCohort writes a cohort with a u8 group key and a nullable
// decimal128 (scale 2). Rows (g, amt): (0, 10.50), (1, -3.25),
// (0, 2.50), (1, 10.50), (0, null), (1, null).
func decimalCountCohort(t *testing.T, fs afero.Fs, path string) {
	t.Helper()
	sch := &encoding.Schema{Fields: []encoding.Field{
		{Name: "g", Type: encoding.FieldTypeU8},
		{Name: "amt", Type: encoding.FieldTypeDecimal128, Nullable: true, Precision: 18, Scale: 2, ByteOffset: 1, CsvColumnIdx: 1},
	}}
	var buf []byte
	w := &byteSink{b: &buf}
	if err := encoding.WriteHeader(w); err != nil {
		t.Fatal(err)
	}
	if err := encoding.WriteSchema(w, sch); err != nil {
		t.Fatal(err)
	}
	for i, m := range []int64{1050, -325, 250, 1050, 0, 0} {
		rec := make([]byte, sch.RecordByteSize())
		rec[0] = byte(i % 2)
		if i >= 4 {
			encoding.BitmapSetNull(rec[len(rec)-sch.BitmapByteSize():], 1)
		} else {
			e := encoding.EncodeDecimal128(encoding.NewDecimal128FromInt(m))
			copy(rec[1:], e[:])
		}
		buf = append(buf, rec...)
	}
	if err := afero.WriteFile(fs, path, buf, 0o644); err != nil {
		t.Fatal(err)
	}
}

// wireOf is the JSON spelling of one emitted figure.
func wireOf(t *testing.T, v any) string {
	t.Helper()
	b, err := types.MarshalFinite(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestDecimalCounts_EmitIntegers (#314): AGG_COUNT, AGG_DISTINCT_COUNT
// and AGG_NULL_COUNT over a decimal128 field emit a JSON INTEGER — a
// count is never a decimal — in every shape: top level, ProcessStream,
// grouped, crosstab cell, crosstab margin and auxiliary margin
// aggregation. AGG_NULL_COUNT reads the null bitmap only. Before, the
// two counts emitted a quoted decimal string ("4") and AGG_NULL_COUNT
// was refused PULSE_AGG_NOT_MEANINGFUL_FOR_DECIMAL.
func TestDecimalCounts_EmitIntegers(t *testing.T) {
	t.Parallel()

	fs := afero.NewMemMapFs()
	decimalCountCohort(t, fs, "dc.pulse")
	p, err := pulse.New(pulse.Options{FS: fs})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	aggs := func() []*types.Aggregation {
		return []*types.Aggregation{
			{Type: types.AGG_COUNT, Field: "amt", Label: "n"},
			{Type: types.AGG_DISTINCT_COUNT, Field: "amt", Label: "d"},
			{Type: types.AGG_NULL_COUNT, Field: "amt", Label: "z"},
		}
	}
	cohort := &types.Cohort{Filename: "dc.pulse"}
	top := map[string]string{"n": "4", "d": "3", "z": "2"}

	resp, err := p.Process(ctx, &types.Request{Cohort: cohort, Aggregations: aggs()})
	if err != nil {
		t.Fatal(err)
	}
	for label, want := range top {
		if got := wireOf(t, resp.Data[0][label]); got != want {
			t.Errorf("top %s = %s, want %s", label, got, want)
		}
	}

	it, err := p.ProcessStream(ctx, &types.Request{Cohort: cohort, Aggregations: aggs()})
	if err != nil {
		t.Fatal(err)
	}
	row, ok, err := it.Next(ctx)
	if err != nil || !ok {
		t.Fatalf("stream: %v %v", ok, err)
	}
	for label, want := range top {
		if got := wireOf(t, row[label]); got != want {
			t.Errorf("stream %s = %s, want %s", label, got, want)
		}
	}
	_ = it.Close()

	grouped, err := p.Process(ctx, &types.Request{Cohort: cohort,
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
		Aggregations: aggs()})
	if err != nil {
		t.Fatal(err)
	}
	if len(grouped.Data) != 2 {
		t.Fatalf("grouped rows = %d", len(grouped.Data))
	}
	for _, r := range grouped.Data {
		for label, want := range map[string]string{"n": "2", "d": "2", "z": "1"} {
			if got := wireOf(t, r[label]); got != want {
				t.Errorf("grouped %v %s = %s, want %s", r["g"], label, got, want)
			}
		}
	}

	for _, cell := range aggs() {
		want := map[string]string{"n": "2", "d": "2", "z": "1"}[cell.Label]
		grand := top[cell.Label]
		ct, err := p.Process(ctx, &types.Request{Cohort: cohort, Crosstab: &types.CrosstabSpec{
			Rows:               []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
			Columns:            []*types.Group{{Type: types.GROUP_CATEGORY, Field: "g"}},
			Cell:               cell,
			MarginAggregations: []*types.Aggregation{{Type: cell.Type, Field: "amt", Label: "aux"}},
			Margins:            types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
		}})
		if err != nil {
			t.Fatalf("%s crosstab: %v", cell.Type, err)
		}
		m := ct.Crosstab.Matrix
		if got := wireOf(t, m.Cells[0][0].Value); got != want {
			t.Errorf("%s cell = %s, want %s", cell.Type, got, want)
		}
		if got := wireOf(t, m.RowMargins[1].Value); got != want {
			t.Errorf("%s row margin = %s, want %s", cell.Type, got, want)
		}
		if got := wireOf(t, m.GrandTotal.Value); got != grand {
			t.Errorf("%s grand total = %s, want %s", cell.Type, got, grand)
		}
		if ct.Components == nil || ct.Components.Crosstab == nil || ct.Components.Crosstab.GrandTotalAggregations == nil {
			t.Fatalf("%s: no auxiliary margin figures", cell.Type)
		}
		// A record reaches an auxiliary margin only if it reached a
		// CELL (present cell field), so the auxiliary null count is 0.
		aux := grand
		if cell.Type == types.AGG_NULL_COUNT {
			aux = "0"
		}
		if got := wireOf(t, ct.Components.Crosstab.GrandTotalAggregations["aux"].Value); got != aux {
			t.Errorf("%s auxiliary grand = %s, want %s", cell.Type, got, aux)
		}
	}
}
