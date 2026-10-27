package processing

import (
	"context"
	stderrors "errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/errors"
	"github.com/frankbardon/pulse/types"
)

// windows_first_grouped_test.go: the grouped and crosstab windows-first
// arms (mmm-followups E4-S3). The windows run over the filtered records
// (partition_by unchanged), their labels land on the records, and the
// groups / crosstab read them as columns: every result equals the same
// request over a cohort carrying the window column (the "twin"), and a
// date-bucketed sum equals the per-record values grouped by hand.

const wfDay0 = 19723 // 2024-01-01 in epoch days

// wfRegions is the categorical source dictionary.
func wfRegions() *encoding.Dictionary {
	d := encoding.NewDictionary()
	for _, s := range []string{"a", "b", "c"} {
		_, _ = d.Add(s)
	}
	return d
}

// wfSchema is d (date), region (categorical), x, plus the twin's
// lag_x (nullable f64).
func wfSchema(twin bool) *encoding.Schema {
	fields := []encoding.Field{
		{Name: "d", Type: encoding.FieldTypeDate},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8, Dictionary: wfRegions()},
		{Name: "x", Type: encoding.FieldTypeF64},
	}
	if twin {
		fields = append(fields, encoding.Field{Name: "lag_x", Type: encoding.FieldTypeF64, Nullable: true})
	}
	return &encoding.Schema{Fields: fields}
}

const wfN = 24

func wfDay(i int) float64    { return float64(wfDay0 + 15*i) }
func wfRegion(i int) float64 { return float64((i * i) % 3) }
func wfX(i int) float64      { return float64((i*7)%11) + 0.25*float64(i) }

// wfLagX is WIN_LAG(x) partitioned by region, ordered by d, by hand:
// the previous record of i's region (d ascends with i); ok false for a
// region's first record.
func wfLagX(i int) (float64, bool) {
	for j := i - 1; j >= 0; j-- {
		if wfRegion(j) == wfRegion(i) {
			return wfX(j), true
		}
	}
	return 0, false
}

// wfQuarter is GROUP_DATE component=quarter's key for record i.
func wfQuarter(i int) string {
	t := time.Unix(int64(wfDay(i))*86400, 0).UTC()
	return fmt.Sprintf("%d-Q%d", t.Year(), (int(t.Month())-1)/3+1)
}

// wfRecords are the source records; twin adds lag_x as a stored column.
func wfRecords(twin bool) []*Record {
	s := wfSchema(twin)
	recs := make([]*Record, wfN)
	for i := range recs {
		vals := map[string]float64{"d": wfDay(i), "region": wfRegion(i), "x": wfX(i)}
		nulls := map[string]bool{}
		if twin {
			if v, ok := wfLagX(i); ok {
				vals["lag_x"] = v
			} else {
				nulls["lag_x"] = true
			}
		}
		recs[i] = NewRecordWithNulls(s, vals, nulls)
	}
	return recs
}

func wfLagXWindow() *types.Window {
	return &types.Window{Type: types.WIN_LAG, Field: "x", Label: "lag_x", PartitionBy: []string{"region"}, OrderBy: []types.OrderKey{{Field: "d"}}}
}

func wfQuarterGroup() *types.Group {
	return &types.Group{Type: types.GROUP_DATE, Field: "d", Params: []byte(`{"component":"quarter"}`)}
}

func wfRegionGroup() *types.Group {
	return &types.Group{Type: types.GROUP_CATEGORY, Field: "region"}
}

// wfRun runs req (buffered) over the window-free source records, or —
// twin — req without its windows over the twin cohort. Crosstab
// requests run RunCrosstab, the rest processRecords.
func wfRun(t *testing.T, req *types.Request, twin bool) *types.Response {
	t.Helper()
	r := *req
	if twin {
		r.Windows = nil
	}
	p := NewProcessor(wfSchema(twin))
	var (
		resp *types.Response
		err  error
	)
	if r.Crosstab != nil {
		resp, err = p.RunCrosstab(context.Background(), &r, wfRecords(twin))
	} else {
		resp, err = p.processRecords(context.Background(), p.prepare(&r), wfRecords(twin))
	}
	if err != nil {
		t.Fatalf("twin=%v: %v", twin, err)
	}
	return resp
}

// wfAssertTwin runs req windows-first and over the twin, and requires
// equal data, crosstab payloads and Components — and a second
// windows-first run to repeat the first exactly (serial determinism).
func wfAssertTwin(t *testing.T, req func() *types.Request) *types.Response {
	t.Helper()
	got := wfRun(t, req(), false)
	want := wfRun(t, req(), true)
	if !reflect.DeepEqual(got.Data, want.Data) {
		t.Fatalf("data\n got %v\nwant %v", got.Data, want.Data)
	}
	if !reflect.DeepEqual(got.Crosstab, want.Crosstab) {
		t.Fatalf("crosstab\n got %+v\nwant %+v", got.Crosstab, want.Crosstab)
	}
	if !reflect.DeepEqual(got.Tests, want.Tests) {
		t.Fatalf("tests\n got %+v\nwant %+v", got.Tests, want.Tests)
	}
	if !reflect.DeepEqual(got.Components, want.Components) {
		t.Fatalf("components\n got %+v\nwant %+v", got.Components, want.Components)
	}
	if again := wfRun(t, req(), false); !reflect.DeepEqual(again, got) {
		t.Fatal("a second windows-first run differs from the first")
	}
	return got
}

// TestWindowsFirst_GroupedSumByDateBucket: a grouped AGG_SUM over a
// partitioned WIN_LAG label, bucketed by quarter, equals the per-record
// lags grouped by hand, and the whole response (data, per-bucket tests,
// grouper and per-bucket aggregation Components in declared order)
// equals the twin.
func TestWindowsFirst_GroupedSumByDateBucket(t *testing.T) {
	t.Parallel()

	req := func() *types.Request {
		return &types.Request{
			Windows: []*types.Window{wfLagXWindow()},
			Groups:  []*types.Group{wfQuarterGroup()},
			Aggregations: []*types.Aggregation{
				{Type: types.AGG_SUM, Field: "lag_x", Label: "s"},
				{Type: types.AGG_COUNT, Field: "x", Label: "n"},
				{Type: types.AGG_AVERAGE, Field: "lag_x", Label: "m"},
			},
			Tests: []*types.Test{{Type: types.TEST_T, Field: "x", Params: []byte(`{"mu":5}`)}},
		}
	}
	resp := wfAssertTwin(t, req)

	want := map[string]float64{}
	for i := 0; i < wfN; i++ {
		if v, ok := wfLagX(i); ok {
			want[wfQuarter(i)] += v
		}
	}
	if len(resp.Data) != len(want) || len(want) != 4 {
		t.Fatalf("rows %v, want one per quarter %v", resp.Data, want)
	}
	for _, row := range resp.Data {
		q, _ := row["d"].(string)
		if got := row["s"].(float64); got != want[q] {
			t.Fatalf("quarter %q sum %v, want hand-grouped %v", q, got, want[q])
		}
		if _, leaked := row["lag_x"]; leaked {
			t.Fatalf("a group row carries the window column: %v", row)
		}
	}
	if len(resp.Tests) == 0 {
		t.Fatal("the per-bucket test emitted nothing")
	}
	var labels []string
	for _, c := range resp.Components.Aggregations {
		labels = append(labels, c.Label)
	}
	if !reflect.DeepEqual(labels, []string{"s", "n", "m"}) {
		t.Fatalf("aggregation Components %v, want declared order", labels)
	}
}

// TestWindowsFirst_GrouperOnWindowLabel: a GROUP_RANGE grouper on the
// window label buckets the records by their lag (null lags in the null
// bucket) exactly as the twin's stored column does.
func TestWindowsFirst_GrouperOnWindowLabel(t *testing.T) {
	t.Parallel()

	wfAssertTwin(t, func() *types.Request {
		return &types.Request{
			Windows:      []*types.Window{wfLagXWindow()},
			Groups:       []*types.Group{{Type: types.GROUP_RANGE, Field: "lag_x", Interval: 3}, wfRegionGroup()},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "s"}},
		}
	})
}

// TestWindowsFirst_CrosstabCellsByDateBucket: a quarter x region
// crosstab of AGG_SUM(lag_x) with every margin equals the twin (payload
// and Components), and each cell is the hand-grouped per-record sum.
func TestWindowsFirst_CrosstabCellsByDateBucket(t *testing.T) {
	t.Parallel()

	resp := wfAssertTwin(t, func() *types.Request {
		return &types.Request{
			Windows: []*types.Window{wfLagXWindow()},
			Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{wfQuarterGroup()},
				Columns: []*types.Group{wfRegionGroup()},
				Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "lag_x", Label: "c"},
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
				Shape:   types.CrosstabShapeMatrix,
			},
		}
	})
	regions := []string{"a", "b", "c"}
	want := map[[2]string]float64{}
	var grand float64
	for i := 0; i < wfN; i++ {
		if v, ok := wfLagX(i); ok {
			want[[2]string{wfQuarter(i), regions[int(wfRegion(i))]}] += v
			grand += v
		}
	}
	m := resp.Crosstab.Matrix
	cells := 0
	for r, rk := range m.RowKeys {
		for c, ck := range m.ColumnKeys {
			w, ok := want[[2]string{fmt.Sprint(rk[0]), fmt.Sprint(ck[0])}]
			if cell := m.Cells[r][c]; ok && cell.Scalar() != w {
				t.Fatalf("cell %v x %v = %v, want hand-grouped %v", rk, ck, cell.Value, w)
			}
			if ok {
				cells++
			}
		}
	}
	if cells != len(want) {
		t.Fatalf("matched %d cells, want %d", cells, len(want))
	}
	if got := m.GrandTotal.Scalar(); got != grand {
		t.Fatalf("grand total %v, want %v", got, grand)
	}
}

// TestWindowsFirst_CrosstabAxisOnWindowLabel: a crosstab axis on the
// window label equals the twin.
func TestWindowsFirst_CrosstabAxisOnWindowLabel(t *testing.T) {
	t.Parallel()

	wfAssertTwin(t, func() *types.Request {
		return &types.Request{
			Windows: []*types.Window{wfLagXWindow()},
			Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{{Type: types.GROUP_RANGE, Field: "lag_x", Interval: 3}},
				Columns: []*types.Group{wfRegionGroup()},
				Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "x", Label: "n"},
				Margins: types.CrosstabMargins{Rows: true, Grand: true},
				Shape:   types.CrosstabShapeMatrix,
			},
		}
	})
}

// TestWindowsFirst_PostWindowAttributeGroupedAndCrosstab: a row-local
// formula over the window label (a post-window attribute) runs after
// the windows and before the groups / crosstab cells, which read its
// label: grouped and crosstab equal the twin, whose formula reads the
// stored column — attribute Components included.
func TestWindowsFirst_PostWindowAttributeGroupedAndCrosstab(t *testing.T) {
	t.Parallel()

	attrs := func() []*types.Attribute {
		return []*types.Attribute{{Type: types.ATTR_FORMULA, Label: "contrib", Expression: "(lag_x ?? 0) * 2 + x"}}
	}
	wfAssertTwin(t, func() *types.Request {
		return &types.Request{
			Windows:      []*types.Window{wfLagXWindow()},
			Attributes:   attrs(),
			Groups:       []*types.Group{wfQuarterGroup()},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "contrib", Label: "s"}},
		}
	})
	wfAssertTwin(t, func() *types.Request {
		return &types.Request{
			Windows:    []*types.Window{wfLagXWindow()},
			Attributes: attrs(),
			Crosstab: &types.CrosstabSpec{
				Rows:    []*types.Group{wfQuarterGroup()},
				Columns: []*types.Group{wfRegionGroup()},
				Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "contrib", Label: "c"},
				Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
				Shape:   types.CrosstabShapeMatrix,
			},
		}
	})
}

// TestWindowsFirst_CategoryGroupOnCategoricalWindowRefused: grouping by
// a window label over a categorical source is refused before any row —
// WIN_LAG / WIN_LEAD accept no categorical field (fieldaccept's window
// table, the one predict reads too), so no window yields a category
// label to key by: PULSE_FIELD_TYPE_UNSUPPORTED naming the window type,
// slot and field type, grouped and crosstab alike.
func TestWindowsFirst_CategoryGroupOnCategoricalWindowRefused(t *testing.T) {
	t.Parallel()

	lag := &types.Window{Type: types.WIN_LAG, Field: "region", Label: "lag_region", OrderBy: []types.OrderKey{{Field: "d"}}}
	for name, req := range map[string]*types.Request{
		"grouped": {Windows: []*types.Window{lag}, Groups: []*types.Group{{Type: types.GROUP_CATEGORY, Field: "lag_region"}},
			Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "x", Label: "s"}}},
		"crosstab": {Windows: []*types.Window{lag}, Crosstab: &types.CrosstabSpec{
			Rows:    []*types.Group{{Type: types.GROUP_CATEGORY, Field: "lag_region"}},
			Columns: []*types.Group{wfRegionGroup()},
			Cell:    &types.Aggregation{Type: types.AGG_COUNT, Field: "x", Label: "n"},
		}},
	} {
		t.Run(name, func(t *testing.T) {
			p := NewProcessor(wfSchema(false))
			var err error
			if req.Crosstab != nil {
				_, err = p.RunCrosstab(context.Background(), req, wfRecords(false))
			} else {
				_, err = p.processRecords(context.Background(), p.prepare(req), wfRecords(false))
			}
			var ce *errors.CodedError
			if !stderrors.As(err, &ce) || ce.Code != errors.PULSE_FIELD_TYPE_UNSUPPORTED ||
				ce.Details["type"] != "WIN_LAG" || ce.Details["field_type"] != "categorical_u8" {
				t.Fatalf("err = %v (details %v), want PULSE_FIELD_TYPE_UNSUPPORTED for WIN_LAG over categorical_u8", err, detailsOf(ce))
			}
		})
	}
}

func detailsOf(ce *errors.CodedError) map[string]any {
	if ce == nil {
		return nil
	}
	return ce.Details
}
