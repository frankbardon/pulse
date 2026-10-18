package pulse

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/frankbardon/pulse/encoding"
	"github.com/frankbardon/pulse/internal/temporal"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// date_key_order_test.go pins calendar-ordered date keys (U50 E2-S1,
// #349 / #350): GROUP_DATE day_of_week emits Monday → Sunday, the
// hour_of_day component emits "00".."23" in numeric order, and
// GROUP_DATE_RANGES emits its ranges in supplied order (unmatched last) —
// on every arm that emits grouped rows (buffered over a file and a shard
// archive, streamed, a Request.Groups composite), in the Components
// buckets, and on both crosstab arms, byte-identically. Group.Include
// restricts both groupers' keys and fixes their emission order; an
// include value no key carries is dropped, never refused, at predict and
// at runtime alike (the GROUP_CATEGORY rule).

var (
	dkoWeekdays = []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}
	dkoHours    = func() []string {
		out := make([]string, 24)
		for h := range out {
			out[h] = fmt.Sprintf("%02d", h)
		}
		return out
	}()
	// dkoRanges lists the ranges neither chronologically nor
	// alphabetically; rows after 2026-03-18 land in "b_none".
	dkoRanges     = `{"ranges":[{"label":"z_late","start":"2026-03-12","end":"2026-03-18"},{"label":"a_early","end":"2026-03-05"},{"label":"m_mid","start":"2026-03-06","end":"2026-03-11"}],"unmatched_label":"b_none"}`
	dkoRangesWant = []string{"z_late", "a_early", "m_mid", "b_none"}
)

// dkoStart is Monday 2026-03-02 00:00 UTC.
var dkoStart = time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)

func dkoSchema() encoding.Schema {
	fields := []encoding.Field{
		{Name: "ts", Type: encoding.FieldTypeDateTime},
		{Name: "region", Type: encoding.FieldTypeCategoricalU8},
		{Name: "s", Type: encoding.FieldTypeF64},
	}
	off := 0
	for i := range fields {
		fields[i].CsvColumnIdx = i
		fields[i].ByteOffset = off
		fields[i].Description = "Date key order column " + fields[i].Name + " (U50 E2-S1 fixture)."
		off += fields[i].Type.ByteSize()
	}
	return encoding.Schema{Fields: fields}
}

// dkoInstants steps 5 hours from dkoStart, 90 times: every weekday and,
// 5 being coprime to 24, every hour of the day, across all three ranges
// and past them.
func dkoInstants() []time.Time {
	out := make([]time.Time, 90)
	for i := range out {
		out[i] = dkoStart.Add(time.Duration(i*5) * time.Hour)
	}
	return out
}

func dkoRows() []CohortRow {
	ts := dkoInstants()
	rows := make([]CohortRow, len(ts))
	for i, t := range ts {
		rows[i] = CohortRow{t.Unix(), []string{"north", "south"}[i%2], float64(i + 1)}
	}
	return rows
}

// dkoEngine builds the fixture as one file and a shard archive.
func dkoEngine(t *testing.T, opts Options) *Pulse {
	t.Helper()
	opts.FS = afero.NewMemMapFs()
	p, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	buildCohort(t, p, "flat.pulse", dkoSchema(), CohortBuilderOptions{}, dkoRows())
	buildCohort(t, p, "arch.pulse", dkoSchema(), CohortBuilderOptions{Shards: &ShardSplit{MaxRecords: 17}}, dkoRows())
	return p
}

func dkoDate(component string, include ...string) func() *types.Group {
	return func() *types.Group {
		return &types.Group{Type: types.GROUP_DATE, Field: "ts", Params: json.RawMessage(`{"component":"` + component + `"}`), Include: include}
	}
}

func dkoDateRanges(include ...string) func() *types.Group {
	return func() *types.Group {
		return &types.Group{Type: types.GROUP_DATE_RANGES, Field: "ts", Params: json.RawMessage(dkoRanges), Include: include}
	}
}

// dkoCase is one grouper configuration and the key order it must emit.
type dkoCase struct {
	name  string
	group func() *types.Group
	want  []string
}

func dkoCases() []dkoCase {
	return []dkoCase{
		{"day_of_week", dkoDate("day_of_week"), dkoWeekdays},
		{"day_of_week/include", dkoDate("day_of_week", "Friday", "Monday", "Funday", "Friday"), []string{"Friday", "Monday"}},
		{"hour_of_day", dkoDate("hour_of_day"), dkoHours},
		{"hour_of_day/include", dkoDate("hour_of_day", "23", "08", "8"), []string{"23", "08"}},
		{"day/include", dkoDate("day", "2026-03-09", "2026-03-02"), []string{"2026-03-09", "2026-03-02"}},
		{"date_ranges", dkoDateRanges(), dkoRangesWant},
		{"date_ranges/include", dkoDateRanges("m_mid", "b_none", "a_early", "nope"), []string{"m_mid", "b_none", "a_early"}},
	}
}

// dkoBuckets is a grouper Components' bucket keys, in emission order.
func dkoBuckets(t *testing.T, resp *types.Response, i int) []string {
	t.Helper()
	if resp.Components == nil || len(resp.Components.Groupers) <= i {
		t.Fatalf("no grouper components at %d", i)
	}
	raw, ok := resp.Components.Groupers[i].Operator["buckets"].([]map[string]any)
	if !ok {
		t.Fatalf("buckets component is %T", resp.Components.Groupers[i].Operator["buckets"])
	}
	out := make([]string, len(raw))
	for j, b := range raw {
		out[j] = fmt.Sprint(b["key"])
	}
	return out
}

// TestDateKeyOrder_GroupedRowsEveryArm: grouped Data rows and the
// grouper's Components buckets follow the calendar / supplied / include
// order — buffered over a file and an archive, and streamed.
func TestDateKeyOrder_GroupedRowsEveryArm(t *testing.T) {
	p := dkoEngine(t, Options{})
	ctx := context.Background()
	for _, tc := range dkoCases() {
		for _, cohort := range []string{"flat.pulse", "arch.pulse"} {
			t.Run(tc.name+"/"+cohort, func(t *testing.T) {
				req := &Request{
					Cohort:       &types.Cohort{Filename: cohort},
					Groups:       []*types.Group{tc.group()},
					Aggregations: []*types.Aggregation{{Type: types.AGG_SUM, Field: "s", Label: "sum"}},
				}
				resp, err := p.Process(ctx, req)
				if err != nil {
					t.Fatal(err)
				}
				if got := columnOf(resp.Data, "ts"); !reflect.DeepEqual(got, tc.want) {
					t.Errorf("buffered rows = %v\nwant %v", got, tc.want)
				}
				if got := dkoBuckets(t, resp, 0); !reflect.DeepEqual(got, tc.want) {
					t.Errorf("components buckets = %v\nwant %v", got, tc.want)
				}
				req.Groups = []*types.Group{tc.group()}
				if got := columnOf(streamRows(t, p, req), "ts"); !reflect.DeepEqual(got, tc.want) {
					t.Errorf("streamed rows = %v\nwant %v", got, tc.want)
				}
			})
		}
	}
}

// TestDateKeyOrder_MultiGrouperComposite: a Request.Groups composite
// orders the date position by its own key order within each region.
func TestDateKeyOrder_MultiGrouperComposite(t *testing.T) {
	p := dkoEngine(t, Options{})
	region := &types.Group{Type: types.GROUP_CATEGORY, Field: "region"}
	for _, tc := range dkoCases() {
		for _, cohort := range []string{"flat.pulse", "arch.pulse"} {
			req := &Request{
				Cohort:       &types.Cohort{Filename: cohort},
				Groups:       []*types.Group{region, tc.group()},
				Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "s", Label: "n"}},
			}
			resp, err := p.Process(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			got := map[string][]string{}
			for _, r := range resp.Data {
				reg := fmt.Sprint(r["region"])
				got[reg] = append(got[reg], fmt.Sprint(r["ts"]))
			}
			for _, reg := range []string{"north", "south"} {
				if !isSubsequence(got[reg], tc.want) || len(got[reg]) == 0 {
					t.Errorf("%s/%s region %s keys = %v, not in order %v", tc.name, cohort, reg, got[reg], tc.want)
				}
			}
		}
	}
}

// isSubsequence reports whether got lists a subset of want in want's order.
func isSubsequence(got, want []string) bool {
	j := 0
	for _, g := range got {
		for j < len(want) && want[j] != g {
			j++
		}
		if j == len(want) {
			return false
		}
		j++
	}
	return true
}

// TestDateKeyOrder_CrosstabBothArms: a crosstab axis over the date
// grouper follows the same order on the fused arm (predict says it is
// taken) and the buffered arm, over a file and an archive, and the two
// arms' crosstab payloads and components are byte-identical.
func TestDateKeyOrder_CrosstabBothArms(t *testing.T) {
	fused := dkoEngine(t, Options{})
	buffered := dkoEngine(t, Options{DisableCrosstabFusion: true})
	ctx := context.Background()
	region := &types.Group{Type: types.GROUP_CATEGORY, Field: "region"}
	for _, tc := range dkoCases() {
		for _, cohort := range []string{"flat.pulse", "arch.pulse"} {
			for _, asRows := range []bool{true, false} {
				name := fmt.Sprintf("%s/%s/rows=%v", tc.name, cohort, asRows)
				build := func() *Request {
					spec := &types.CrosstabSpec{
						Rows:    []*types.Group{tc.group()},
						Columns: []*types.Group{region},
						Cell:    &types.Aggregation{Type: types.AGG_SUM, Field: "s", Label: "sum"},
						Margins: types.CrosstabMargins{Rows: true, Columns: true, Grand: true},
					}
					if !asRows {
						spec.Rows, spec.Columns = spec.Columns, spec.Rows
					}
					return &Request{Cohort: &types.Cohort{Filename: cohort}, Crosstab: spec}
				}
				if cohort == "flat.pulse" {
					pr, err := fused.Predict(ctx, build())
					if err != nil {
						t.Fatal(err)
					}
					if pr.CrosstabFusable == nil || !*pr.CrosstabFusable {
						t.Fatalf("%s: crosstab not fusable (%v) — the fused arm is untested", name, pr.CrosstabFusionReasons)
					}
				}
				fr, err := fused.Process(ctx, build())
				if err != nil {
					t.Fatalf("%s fused: %v", name, err)
				}
				br, err := buffered.Process(ctx, build())
				if err != nil {
					t.Fatalf("%s buffered: %v", name, err)
				}
				keys := fr.Crosstab.Matrix.RowKeys
				if !asRows {
					keys = fr.Crosstab.Matrix.ColumnKeys
				}
				if got := axisStrings(keys, 0); !reflect.DeepEqual(got, tc.want) {
					t.Errorf("%s fused axis = %v\nwant %v", name, got, tc.want)
				}
				fj, _ := json.Marshal(fr.Crosstab)
				bj, _ := json.Marshal(br.Crosstab)
				if string(fj) != string(bj) {
					t.Errorf("%s: fused and buffered crosstab differ:\nfused    %s\nbuffered %s", name, fj, bj)
				}
				fc, _ := json.Marshal(fr.Components.Crosstab)
				bc, _ := json.Marshal(br.Components.Crosstab)
				if string(fc) != string(bc) {
					t.Errorf("%s: fused and buffered crosstab components differ:\nfused    %s\nbuffered %s", name, fc, bc)
				}
			}
		}
	}
}

// TestDateKeyOrder_HourOfDayZoneAware: hour_of_day buckets the local
// wall-clock hour of the resolved zone — a New York run shifts every
// count by the UTC offset of its instant, as a time.In reference does.
func TestDateKeyOrder_HourOfDayZoneAware(t *testing.T) {
	p := dkoEngine(t, Options{})
	zone, err := temporal.LoadZone("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	ny := zone.Location()
	want := map[string]float64{}
	for _, ts := range dkoInstants() {
		want[ts.In(ny).Format("15")]++
	}
	g := dkoDate("hour_of_day")()
	g.TimeZone = "America/New_York"
	resp, err := p.Process(context.Background(), &Request{
		Cohort:       &types.Cohort{Filename: "flat.pulse"},
		Groups:       []*types.Group{g},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "s", Label: "n"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]float64{}
	var order []string
	for _, r := range resp.Data {
		k := fmt.Sprint(r["ts"])
		order = append(order, k)
		got[k], _ = toFloat(r["n"])
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("New York hour_of_day counts = %v\nwant %v", got, want)
	}
	if !reflect.DeepEqual(order, dkoHours) {
		t.Errorf("New York hour_of_day order = %v", order)
	}
}

// TestDateKeyOrder_IncludePredictMatchesRuntime: an include value no key
// carries (a misspelt weekday, an unpadded hour, an unknown range label)
// is dropped, never refused — predict is valid and runtime runs, the
// GROUP_CATEGORY include rule; a hour_of_day over a date field is
// refused PROCESSING_CONFIG by both.
func TestDateKeyOrder_IncludePredictMatchesRuntime(t *testing.T) {
	p := dkoEngine(t, Options{})
	ctx := context.Background()
	for _, g := range []*types.Group{
		dkoDate("day_of_week", "Funday")(),
		dkoDate("hour_of_day", "7", "24")(),
		dkoDateRanges("nope")(),
	} {
		req := &Request{
			Cohort:       &types.Cohort{Filename: "flat.pulse"},
			Groups:       []*types.Group{g},
			Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "s", Label: "n"}},
		}
		pr, err := p.Predict(ctx, req)
		if err != nil || !pr.Valid {
			t.Fatalf("%s include %v: predict err=%v valid=%v", g.Type, g.Include, err, pr != nil && pr.Valid)
		}
		resp, err := p.Process(ctx, req)
		if err != nil {
			t.Fatalf("%s include %v: %v", g.Type, g.Include, err)
		}
		if len(resp.Data) != 0 {
			t.Errorf("%s include %v: rows %v, want none", g.Type, g.Include, resp.Data)
		}
	}
}
