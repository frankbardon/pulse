package pulse

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	pio "github.com/frankbardon/pulse/io"
	"github.com/frankbardon/pulse/types"
	"github.com/spf13/afero"
)

// preEpochCSV straddles the Unix epoch on both temporal types. The date
// word is two's-complement int32 days and the datetime word int64
// seconds, so every row below must survive decode, filter, group,
// aggregate, sort, export and lookup with its sign intact.
const preEpochCSV = "id,d,ts\n" +
	"1,1900-01-01,1900-01-01T06:00:00Z\n" +
	"2,1969-12-31,1969-12-31T23:59:59Z\n" +
	"3,1970-01-01,1970-01-01T00:00:00Z\n" +
	"4,2024-02-29,2024-02-29T12:30:00Z\n"

// preEpochPulse imports preEpochCSV into a hermetic cohort and returns
// the engine over it.
func preEpochPulse(t *testing.T) (*Pulse, afero.Fs) {
	t.Helper()
	memFs := afero.NewMemMapFs()
	importCSVBytes(t, memFs, []byte(preEpochCSV), "pre.pulse")
	p, err := New(Options{FS: memFs})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	res, err := p.Inspect(context.Background(), "pre.pulse")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	types := map[string]string{}
	for _, f := range res.Fields {
		types[f.Name] = f.Type
	}
	if types["d"] != "date" || types["ts"] != "datetime" {
		t.Fatalf("inferred types = %v, want d=date ts=datetime", types)
	}
	return p, memFs
}

func importCSVBytes(t *testing.T, fs afero.Fs, data []byte, target string) {
	t.Helper()
	r, err := pio.NewReaderFromBytes(pio.FormatCSV, data, pio.ReaderOptions{})
	if err != nil {
		t.Fatalf("NewReaderFromBytes: %v", err)
	}
	job := pio.NewImportJob(r, target)
	job.FS = fs
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("import %s: %v", target, err)
	}
}

func exportBytes(t *testing.T, fs afero.Fs, source string, f pio.Format) []byte {
	t.Helper()
	w, err := pio.NewWriterToBuffer(f, pio.WriterOptions{})
	if err != nil {
		t.Fatalf("NewWriterToBuffer(%s): %v", f, err)
	}
	job := pio.NewExportJob(source, w)
	job.FS = fs
	if _, err := job.Run(context.Background()); err != nil {
		t.Fatalf("export %s as %s: %v", source, f, err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close %s writer: %v", f, err)
	}
	return w.Bytes()
}

func processRows(t *testing.T, p *Pulse, req *types.Request) []map[string]any {
	t.Helper()
	if req.Cohort == nil {
		req.Cohort = &types.Cohort{Filename: "pre.pulse"}
	}
	resp, err := p.Process(context.Background(), req)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	return resp.Data
}

func countOf(t *testing.T, p *Pulse, filters ...*types.Filterer) float64 {
	t.Helper()
	rows := processRows(t, p, &types.Request{
		Filterers:    filters,
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "id", Label: "n"}},
	})
	if len(rows) != 1 {
		t.Fatalf("count rows = %v, want one", rows)
	}
	return rows[0]["n"].(float64)
}

func bucketCounts(t *testing.T, p *Pulse, g *types.Group) map[string]float64 {
	t.Helper()
	rows := processRows(t, p, &types.Request{
		Groups:       []*types.Group{g},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "id", Label: "n"}},
	})
	out := map[string]float64{}
	for _, r := range rows {
		out[fmt.Sprint(r[g.Field])] = r["n"].(float64)
	}
	return out
}

func wantBuckets(t *testing.T, name string, got, want map[string]float64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s buckets = %v, want %v", name, got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s buckets = %v, want %v", name, got, want)
		}
	}
}

// TestPreEpoch_CSVExportRoundTrip pins the text writer: every date and
// datetime prints back exactly as imported.
func TestPreEpoch_CSVExportRoundTrip(t *testing.T) {
	_, fs := preEpochPulse(t)
	if got := string(exportBytes(t, fs, "pre.pulse", pio.FormatCSV)); got != preEpochCSV {
		t.Fatalf("CSV export =\n%s\nwant\n%s", got, preEpochCSV)
	}
	nd := string(exportBytes(t, fs, "pre.pulse", pio.FormatNDJSON))
	for _, lit := range []string{"1900-01-01", "1969-12-31", "1969-12-31T23:59:59Z", "1900-01-01T06:00:00Z", "2024-02-29"} {
		if !strings.Contains(nd, `"`+lit) {
			t.Errorf("NDJSON export lacks %q:\n%s", lit, nd)
		}
	}
}

// TestPreEpoch_FiltersAcrossTheEpoch pins FILTER_RANGE on the signed day
// and FILTER_DATE_RANGES with a pre-1970 start and post-1970 end.
func TestPreEpoch_FiltersAcrossTheEpoch(t *testing.T) {
	p, _ := preEpochPulse(t)
	if n := countOf(t, p, &types.Filterer{Type: types.FILTER_RANGE, Field: "d", Values: []string{"-1", "0"}}); n != 2 {
		t.Errorf("FILTER_RANGE d in [-1,0] kept %v, want 2 (1969-12-31, 1970-01-01)", n)
	}
	if n := countOf(t, p, &types.Filterer{Type: types.FILTER_RANGE, Field: "d", Values: []string{"-30000", "-1"}}); n != 2 {
		t.Errorf("FILTER_RANGE d in [-30000,-1] kept %v, want 2 (both pre-1970 rows)", n)
	}
	if n := countOf(t, p, &types.Filterer{Type: types.FILTER_RANGE, Field: "ts", Values: []string{"-1", "0"}}); n != 2 {
		t.Errorf("FILTER_RANGE ts in [-1,0] kept %v, want 2", n)
	}
	across := json.RawMessage(`{"ranges":[{"label":"span","start":"1969-12-01","end":"1970-01-31"}]}`)
	for _, field := range []string{"d", "ts"} {
		if n := countOf(t, p, &types.Filterer{Type: types.FILTER_DATE_RANGES, Field: field, Params: across}); n != 2 {
			t.Errorf("FILTER_DATE_RANGES %s across the epoch kept %v, want 2", field, n)
		}
	}
	pre := json.RawMessage(`{"ranges":[{"label":"old","end":"1969-12-31"}]}`)
	if n := countOf(t, p, &types.Filterer{Type: types.FILTER_DATE_RANGES, Field: "d", Params: pre}); n != 2 {
		t.Errorf("FILTER_DATE_RANGES open-lower ..1969-12-31 kept %v, want 2", n)
	}
}

// TestPreEpoch_GroupersAcrossTheEpoch pins GROUP_DATE day/month/year on
// both types (1969-12-31T23:59:59Z floors to day -1) and GROUP_DATE_RANGES.
func TestPreEpoch_GroupersAcrossTheEpoch(t *testing.T) {
	p, _ := preEpochPulse(t)
	for _, field := range []string{"d", "ts"} {
		wantBuckets(t, field+" day", bucketCounts(t, p, &types.Group{Type: types.GROUP_DATE, Field: field, Params: json.RawMessage(`{"component":"day"}`)}),
			map[string]float64{"1900-01-01": 1, "1969-12-31": 1, "1970-01-01": 1, "2024-02-29": 1})
		wantBuckets(t, field+" year", bucketCounts(t, p, &types.Group{Type: types.GROUP_DATE, Field: field, Params: json.RawMessage(`{"component":"year"}`)}),
			map[string]float64{"1900": 1, "1969": 1, "1970": 1, "2024": 1})
		months := bucketCounts(t, p, &types.Group{Type: types.GROUP_DATE, Field: field, Params: json.RawMessage(`{"component":"month"}`)})
		if len(months) != 4 {
			t.Errorf("%s month buckets = %v, want 4 distinct months", field, months)
		}
		for k := range months {
			if !strings.HasPrefix(k, "1900") && !strings.HasPrefix(k, "1969") && !strings.HasPrefix(k, "1970") && !strings.HasPrefix(k, "2024") {
				t.Errorf("%s month bucket %q is not a real month", field, k)
			}
		}
		ranges := json.RawMessage(`{"ranges":[{"label":"pre","end":"1969-12-31"},{"label":"post","start":"1970-01-01"}]}`)
		wantBuckets(t, field+" ranges", bucketCounts(t, p, &types.Group{Type: types.GROUP_DATE_RANGES, Field: field, Params: ranges}),
			map[string]float64{"pre": 2, "post": 2})
	}
}

// TestPreEpoch_AggregatesAttributesAndSort pins AGG_MIN/MAX, ATTR_DATE_PART
// year and Request.Sort ordering on the signed day.
func TestPreEpoch_AggregatesAttributesAndSort(t *testing.T) {
	p, _ := preEpochPulse(t)
	rows := processRows(t, p, &types.Request{Aggregations: []*types.Aggregation{
		{Type: types.AGG_MIN, Field: "d", Label: "dmin"},
		{Type: types.AGG_MAX, Field: "d", Label: "dmax"},
		{Type: types.AGG_MIN, Field: "ts", Label: "tmin"},
	}})
	if got := rows[0]["dmin"]; got != float64(-25567) {
		t.Errorf("AGG_MIN d = %v, want -25567 (1900-01-01)", got)
	}
	if got := rows[0]["dmax"]; got != float64(19782) {
		t.Errorf("AGG_MAX d = %v, want 19782 (2024-02-29)", got)
	}
	if got := rows[0]["tmin"]; got != float64(-2208967200) {
		t.Errorf("AGG_MIN ts = %v, want -2208967200 (1900-01-01T06:00:00Z)", got)
	}

	years := processRows(t, p, &types.Request{
		Attributes:   []*types.Attribute{{Type: types.ATTR_DATE_PART, Field: "d", Label: "yr", Params: json.RawMessage(`{"part":"year"}`)}},
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "yr"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_COUNT, Field: "id", Label: "n"}},
	})
	var gotYears []string
	for _, r := range years {
		gotYears = append(gotYears, fmt.Sprint(r["yr"]))
	}
	sort.Strings(gotYears)
	if strings.Join(gotYears, ",") != "1900,1969,1970,2024" {
		t.Errorf("ATTR_DATE_PART year = %v, want 1900,1969,1970,2024", gotYears)
	}

	sorted := processRows(t, p, &types.Request{
		Groups:       []*types.Group{{Type: types.GROUP_CATEGORY, Field: "id"}},
		Aggregations: []*types.Aggregation{{Type: types.AGG_MIN, Field: "d", Label: "day"}},
		Sort:         []types.OrderKey{{Field: "day"}},
	})
	var order []string
	for _, r := range sorted {
		order = append(order, fmt.Sprint(r["id"]))
	}
	if strings.Join(order, ",") != "1,2,3,4" {
		t.Errorf("sort by day = %v, want 1,2,3,4 (1900 < 1969 < 1970 < 2024)", order)
	}
}

// TestPreEpoch_PointLookup pins the sidecar index key on a pre-1970 date
// and datetime: build-path and probe-path bytes must agree on every
// platform (a float64->uint64 conversion of a negative is
// architecture-defined, so the encoder must go through int32/int64).
func TestPreEpoch_PointLookup(t *testing.T) {
	p, _ := preEpochPulse(t)
	ctx := context.Background()
	for _, tc := range []struct{ field, value, wantID string }{
		{"d", "1900-01-01", "1"},
		{"d", "1969-12-31", "2"},
		{"d", "2024-02-29", "4"},
		{"ts", "1969-12-31T23:59:59Z", "2"},
	} {
		if _, err := p.BuildIndex(ctx, "pre.pulse", []string{tc.field}); err != nil {
			t.Fatalf("BuildIndex(%s): %v", tc.field, err)
		}
		res, err := p.Lookup(ctx, &LookupRequest{Cohort: &types.Cohort{Filename: "pre.pulse"}, Field: tc.field, Value: tc.value})
		if err != nil {
			t.Fatalf("Lookup %s=%s: %v", tc.field, tc.value, err)
		}
		if len(res.Rows) != 1 || fmt.Sprint(res.Rows[0]["id"]) != tc.wantID {
			t.Fatalf("Lookup %s=%s rows = %v, want id %s", tc.field, tc.value, res.Rows, tc.wantID)
		}
	}
}

// TestPreEpoch_ImportInfersDateForTimeOfDayLiteral pins what inference
// does with a pre-1970 `1969-12-31T12:00:00` in an otherwise date-only
// column: it stays `date` and day-truncates, and export prints the day.
func TestPreEpoch_ImportInfersDateForTimeOfDayLiteral(t *testing.T) {
	memFs := afero.NewMemMapFs()
	importCSVBytes(t, memFs, []byte("id,d\n1,1900-01-01\n2,1969-12-31T12:00:00\n3,1969-12-31\n"), "mixed.pulse")
	got := string(exportBytes(t, memFs, "mixed.pulse", pio.FormatCSV))
	want := "id,d\n1,1900-01-01\n2,1969-12-31\n3,1969-12-31\n"
	if got != want {
		t.Fatalf("export =\n%s\nwant\n%s", got, want)
	}
}

// TestPreEpoch_AdapterRoundTrips sends the cohort through every binary or
// typed adapter that carries dates and back: the re-imported cohort must
// export the same CSV.
func TestPreEpoch_AdapterRoundTrips(t *testing.T) {
	for _, f := range []pio.Format{pio.FormatArrow, pio.FormatParquet, pio.FormatExcel, pio.FormatSPSS, pio.FormatNDJSON, pio.FormatJSONArray, pio.FormatTSV} {
		t.Run(string(f), func(t *testing.T) {
			_, fs := preEpochPulse(t)
			data := exportBytes(t, fs, "pre.pulse", f)
			r, err := pio.NewReaderFromBytes(f, data, pio.ReaderOptions{})
			if err != nil {
				t.Fatalf("NewReaderFromBytes: %v", err)
			}
			job := pio.NewImportJob(r, "back.pulse")
			job.FS = fs
			if _, err := job.Run(context.Background()); err != nil {
				t.Fatalf("re-import: %v", err)
			}
			got := string(exportBytes(t, fs, "back.pulse", pio.FormatCSV))
			if got != preEpochCSV {
				t.Fatalf("%s round trip =\n%s\nwant\n%s", f, got, preEpochCSV)
			}
		})
	}
}
